// Package jev talks to Jev, the only service qualm calls over the network. It builds the request,
// retries transient failures, reads the answers, and keeps the cache and the budget.
package jev

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/tools4imps/qualm/internal/questions"
)

// Endpoint is where Jev is served.
const Endpoint = "https://openrouter.ai/api/alpha/decisions"

// KeyVar names the environment variable that holds the API key.
const KeyVar = "OPENROUTER_API_KEY"

// pricePerMillion is what Jev charges for a million input tokens, in dollars.
const pricePerMillion = 0.042

// CostOf prices input tokens in dollars. It is the cost of a reply that reports none, and the
// estimate for a request that is not sent.
func CostOf(inputTokens int) float64 { return float64(inputTokens) * pricePerMillion / 1e6 }

// TokensIn estimates the input tokens in that many bytes of a request, at three bytes a token.
func TokensIn(bytes int) int { return bytes / 3 }

const (
	maxAttempts  = 4
	maxReplySize = 4 << 20
)

// Request is one call to Jev. Its JSON is also what the cache key is made from.
type Request struct {
	Model     string         `json:"model"`
	State     map[string]any `json:"state"`
	Questions map[string]any `json:"questions"`
}

// Answer is Jev's reply to one question, as a number from 0 to 1.
type Answer struct {
	Value         float64            `json:"value"` // for a choice, the probability of the chosen option
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// Reply is what qualm keeps of a response: the answers, the input tokens and the cost in dollars.
// It is also what the cache stores.
type Reply struct {
	Answers     map[string]Answer `json:"answers"`
	InputTokens int               `json:"input_tokens"`
	Cost        float64           `json:"cost"`
}

// httpClient is shared by every request, so connections are reused. The timeout is for one attempt.
var httpClient = &http.Client{Timeout: 90 * time.Second}

// Client calls Jev. The key arrives through Key so this package never reads the environment.
type Client struct {
	Endpoint string
	Key      string
	Sleep    func(time.Duration) // nil for a sleep that ends early when ctx does
}

// Key is a request's cache key: the SHA-256 of its JSON, in hex. encoding/json sorts map keys,
// which keeps the bytes the same however the maps were filled.
func Key(req Request) string {
	data, _ := json.Marshal(req) // maps of strings and plain values always marshal
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// retryable are the statuses Jev answers with when it is busy and a later attempt may succeed.
var retryable = map[int]bool{429: true, 500: true, 502: true, 503: true, 504: true, 529: true}

// Ask sends one request and reads the answers to the questions asked.
func (c *Client) Ask(ctx context.Context, req Request, asked []questions.Question) (Reply, error) {
	if c.Key == "" {
		return Reply{}, errors.New("no API key: set " + KeyVar)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Reply{}, fmt.Errorf("jev: encoding the request: %w", err)
	}

	var lastErr error
	wait := time.Second
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Reply{}, fmt.Errorf("jev: %w", err)
		}
		raw, status, err := c.once(ctx, body)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return Reply{}, fmt.Errorf("jev: %w", ctx.Err())
			}
			lastErr = err
		case status == http.StatusOK:
			return parseReply(raw, asked, len(body))
		case retryable[status]:
			lastErr = fmt.Errorf("jev answered HTTP %d", status)
		default:
			return Reply{}, statusError(status)
		}
		if attempt < maxAttempts {
			c.sleep(ctx, wait)
			wait *= 2
		}
	}
	return Reply{}, fmt.Errorf("jev failed after %d attempts: %w", maxAttempts, lastErr)
}

// statusError names the status and nothing from the reply, which could echo secrets back.
func statusError(status int) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("jev answered HTTP %d: the API key was refused", status)
	}
	return fmt.Errorf("jev answered HTTP %d", status)
}

func (c *Client) sleep(ctx context.Context, d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// once makes a single attempt and returns the reply body only when the status is 200.
func (c *Client) once(ctx context.Context, body []byte) ([]byte, int, error) {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, cmp.Or(c.Endpoint, Endpoint), bytes.NewReader(body))
	if err != nil {
		return nil, 0, errors.New("jev: the endpoint is not a valid URL")
	}
	hreq.Header.Set("Authorization", "Bearer "+c.Key)
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(hreq)
	if err != nil {
		return nil, 0, transportError(err, "jev: the request could not be completed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // lets the connection be reused
		return nil, resp.StatusCode, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReplySize+1))
	if err != nil {
		return nil, 0, transportError(err, "jev: the reply could not be read")
	}
	if len(data) > maxReplySize {
		return nil, 0, errors.New("jev: the reply is too large")
	}
	return data, resp.StatusCode, nil
}

// transportError says a request timed out when it did, and otherwise what the caller says of it.
// The cause is a *url.Error that carries the URL and no headers, but nothing of its text is kept,
// to be safe about what an odd endpoint could put in it.
func transportError(cause error, otherwise string) error {
	var timeout net.Error
	if errors.As(cause, &timeout) && timeout.Timeout() {
		return errors.New("jev: the request timed out")
	}
	return errors.New(otherwise)
}

type rawAnswer struct {
	Noul          *float64           `json:"noul"`
	Score         *float64           `json:"score"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
}

type rawReply struct {
	Answers map[string]rawAnswer `json:"answers"`
	Usage   rawUsage             `json:"usage"`
}

type rawUsage struct {
	InputTokens int      `json:"input_tokens"`
	Cost        *float64 `json:"cost"`
}

// spent is the input tokens and the dollars a reply cost. One that reports no cost is priced by
// its tokens. One that reports neither is priced by the size of the request, sent bytes, so that
// the budget still counts it.
func (u rawUsage) spent(sent int) (inputTokens int, cost float64) {
	if u.Cost != nil && *u.Cost > 0 {
		return u.InputTokens, *u.Cost
	}
	inputTokens = cmp.Or(u.InputTokens, TokensIn(sent))
	return inputTokens, CostOf(inputTokens)
}

func inUnit(v float64) bool { return v >= 0 && v <= 1 }

// parseReply reads the reply to a request of sent bytes. It never quotes the body in its errors.
func parseReply(data []byte, asked []questions.Question, sent int) (Reply, error) {
	var raw rawReply
	if err := json.Unmarshal(data, &raw); err != nil {
		return Reply{}, errors.New("jev: the reply is not the JSON that was expected")
	}
	out := Reply{Answers: make(map[string]Answer, len(asked))}
	out.InputTokens, out.Cost = raw.Usage.spent(sent)

	for _, q := range asked {
		a, ok := raw.Answers[q.ID]
		if !ok {
			return Reply{}, fmt.Errorf("jev: the reply has no answer for question %q", q.ID)
		}
		switch q.Type {
		case questions.TypeNoul:
			if a.Noul == nil || !inUnit(*a.Noul) {
				return Reply{}, fmt.Errorf("jev: question %q has no probability between 0 and 1", q.ID)
			}
			out.Answers[q.ID] = Answer{Value: *a.Noul}
		case questions.TypeScore:
			if a.Score == nil {
				return Reply{}, fmt.Errorf("jev: question %q has no score", q.ID)
			}
			// With a single level the top is 0 and the division gives NaN or an infinity, which
			// is outside 0 to 1, so that case needs no check of its own.
			v := *a.Score / float64(q.Levels()-1)
			if !inUnit(v) {
				return Reply{}, fmt.Errorf("jev: the score for question %q is outside its levels", q.ID)
			}
			out.Answers[q.ID] = Answer{Value: v}
		case questions.TypeChoice:
			p, ok := a.Probabilities[a.Choice]
			if !ok {
				return Reply{}, fmt.Errorf("jev: question %q has a choice with no probability", q.ID)
			}
			for _, v := range a.Probabilities {
				if !inUnit(v) {
					return Reply{}, fmt.Errorf("jev: a probability for question %q is outside 0 to 1", q.ID)
				}
			}
			out.Answers[q.ID] = Answer{Value: p, Choice: a.Choice, Probabilities: a.Probabilities}
		default:
			return Reply{}, fmt.Errorf("jev: question %q has unknown type %q", q.ID, q.Type)
		}
	}
	return out, nil
}
