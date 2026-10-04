// Package jev talks to Jev, the only service qualm calls over the network. It builds the request,
// retries transient failures, reads the answers, and keeps the cache and the budget.
package jev

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tools4imps/qualm/internal/questions"
)

// Endpoint is where Jev is served.
const Endpoint = "https://openrouter.ai/api/alpha/decisions"

// PricePerMillion is what Jev charges for a million input tokens, in dollars.
const PricePerMillion = 0.042

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

// Client calls Jev. The key arrives through Key so this package never reads the environment.
type Client struct {
	Endpoint string
	Key      string
	HTTP     *http.Client        // nil for a client with a 90 second timeout
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
		return Reply{}, errors.New("no API key: set OPENROUTER_API_KEY")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Reply{}, fmt.Errorf("jev: encoding the request: %w", err)
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = &http.Client{Timeout: 90 * time.Second}
	}

	var lastErr error
	wait := time.Second
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Reply{}, fmt.Errorf("jev: %w", err)
		}
		raw, status, err := c.once(ctx, httpc, body)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return Reply{}, fmt.Errorf("jev: %w", ctx.Err())
			}
			lastErr = err
		case status == http.StatusOK:
			return parseReply(raw, asked)
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
func (c *Client) once(ctx context.Context, httpc *http.Client, body []byte) ([]byte, int, error) {
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = Endpoint
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, errors.New("jev: the endpoint is not a valid URL")
	}
	hreq.Header.Set("Authorization", "Bearer "+c.Key)
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Do(hreq)
	if err != nil {
		// The cause of a transport error is a *url.Error that carries the URL and no headers, but
		// only its type is kept, to be safe about what an odd endpoint could put in it.
		return nil, 0, errors.New("jev: the request could not be completed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // lets the connection be reused
		return nil, resp.StatusCode, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReplySize+1))
	if err != nil {
		return nil, 0, errors.New("jev: the reply could not be read")
	}
	if len(data) > maxReplySize {
		return nil, 0, errors.New("jev: the reply is too large")
	}
	return data, resp.StatusCode, nil
}

type rawAnswer struct {
	Noul          *float64           `json:"noul"`
	Score         *float64           `json:"score"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
}

type rawReply struct {
	Answers map[string]rawAnswer `json:"answers"`
	Usage   struct {
		InputTokens int      `json:"input_tokens"`
		Cost        *float64 `json:"cost"`
	} `json:"usage"`
}

func inUnit(v float64) bool { return v >= 0 && v <= 1 }

// parseReply never quotes the body in its errors.
func parseReply(data []byte, asked []questions.Question) (Reply, error) {
	var raw rawReply
	if err := json.Unmarshal(data, &raw); err != nil {
		return Reply{}, errors.New("jev: the reply is not the JSON that was expected")
	}
	out := Reply{Answers: make(map[string]Answer, len(asked)), InputTokens: raw.Usage.InputTokens}
	if raw.Usage.Cost != nil && *raw.Usage.Cost > 0 {
		out.Cost = *raw.Usage.Cost
	} else {
		out.Cost = float64(raw.Usage.InputTokens) * PricePerMillion / 1e6
	}

	for _, q := range asked {
		a, ok := raw.Answers[q.ID]
		if !ok {
			return Reply{}, fmt.Errorf("jev: the reply has no answer for question %q", q.ID)
		}
		switch q.Type {
		case "noul":
			if a.Noul == nil || !inUnit(*a.Noul) {
				return Reply{}, fmt.Errorf("jev: question %q has no probability between 0 and 1", q.ID)
			}
			out.Answers[q.ID] = Answer{Value: *a.Noul}
		case "score":
			top := q.Levels() - 1
			if top < 1 {
				return Reply{}, fmt.Errorf("jev: question %q has fewer than two levels", q.ID)
			}
			if a.Score == nil {
				return Reply{}, fmt.Errorf("jev: question %q has no score", q.ID)
			}
			v := *a.Score / float64(top)
			if !inUnit(v) {
				return Reply{}, fmt.Errorf("jev: the score for question %q is outside its levels", q.ID)
			}
			out.Answers[q.ID] = Answer{Value: v}
		case "choice":
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
