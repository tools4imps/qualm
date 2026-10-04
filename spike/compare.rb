#!/usr/bin/env ruby
# frozen_string_literal: true

# A spike: instead of scoring a file twice and subtracting, show Jev the change and ask about it.
# Each modified file on a repository's mainline is asked about two ways: with both whole versions
# side by side ("pair"), and with only the diff ("diff").
#
#   OPENROUTER_API_KEY=... ruby compare.rb REPO [--rev origin/main] [--paths 'lib/*.rb,lib/**/*.rb']
#                                               [--out compare.json] [--limit N]
require "open3"
require "optparse"
require_relative "jev"

PAIR_BYTES = 100_000 # both versions together have to fit in Jev's 32,000 tokens of state

options = { rev: "origin/main", paths: ["lib/*.rb", "lib/**/*.rb"], out: File.join(__dir__, "compare.json"), limit: nil }
repo, = OptionParser.new do |o|
  o.on("--rev REV") { |v| options[:rev] = v }
  o.on("--paths GLOBS") { |v| options[:paths] = v.split(",") }
  o.on("--out FILE") { |v| options[:out] = v }
  o.on("--limit N", Integer) { |v| options[:limit] = v }
end.parse(ARGV)
abort "Which repository?" unless repo

git = lambda do |*args|
  out, status = Open3.capture2("git", "-C", repo, *args, err: File::NULL)
  status.success? ? out.force_encoding(Encoding::UTF_8).scrub : nil
end

questions = JSON.parse(File.read(File.join(__dir__, "questions-change.json")))
wire = Spike.wire(questions)

log = git.call("log", "--first-parent", "--reverse", "--format=%H%x09%ad%x09%s", "--date=short", options[:rev],
               "--", *options[:paths]).lines(chomp: true)
log = log.last(options[:limit]) if options[:limit]

jobs = []
commits = log.filter_map do |line|
  sha, date, subject = line.split("\t", 3)
  next unless git.call("rev-parse", "--verify", "--quiet", "#{sha}^1")

  changes = git.call("diff", "--name-status", "-M", "#{sha}^1", sha, "--", *options[:paths]).lines(chomp: true).filter_map do |row|
    status, old, new = row.split("\t")
    new ||= old
    next unless %w[M R].include?(status[0])

    before = git.call("show", "#{sha}^1:#{old}")
    after = git.call("show", "#{sha}:#{new}")
    next if before == after

    change = { "path" => new, "lines" => [before.count("\n"), after.count("\n")] }
    jobs << [change, "diff", { "language" => Spike.language_for(new),
                               "format" => "A unified diff of one file. Lines starting with - are the earlier version, " \
                                           "lines starting with + are the later version.",
                               "change" => git.call("diff", "-U8", "#{sha}^1:#{old}", "#{sha}:#{new}") }]
    if before.bytesize + after.bytesize <= PAIR_BYTES
      jobs << [change, "pair", { "language" => Spike.language_for(new), "earlier_version" => before, "later_version" => after }]
    end
    change
  end
  type = subject[/\A([a-z]+)(\([^)]*\))?!?:/, 1] || (subject.start_with?("Merge") ? "merge" : "other")
  { "sha" => sha[0, 7], "date" => date, "type" => type, "subject" => subject, "changes" => changes }
end
warn "#{commits.size} commits, #{commits.sum { |c| c["changes"].size }} modified files, #{jobs.size} requests"

tokens = 0
cost = 0.0
done = 0
lock = Mutex.new
Spike.pool(jobs, workers: 8) do |change, way, state|
  reply, replayed = Spike.ask({ "model" => Spike::MODEL, "state" => state, "questions" => wire })
  answers = questions.to_h do |q|
    a = reply.fetch("answers").fetch(q["id"])
    [q["id"], q["type"] == "choice" ? a.fetch("probabilities").merge("choice" => a.fetch("choice")) : a.fetch("noul")]
  end
  lock.synchronize do
    change[way] = answers
    unless replayed
      tokens += reply.dig("usage", "input_tokens").to_i
      cost += reply.dig("usage", "cost").to_f
    end
    done += 1
    warn "  #{done}/#{jobs.size}" if (done % 100).zero?
  end
end

File.write(options[:out], JSON.pretty_generate({ "repo" => repo, "rev" => options[:rev], "model" => Spike::MODEL,
                                                 "questions" => questions.map { |q| q["id"] }, "commits" => commits }))
warn format("%d input tokens billed, $%.4f. Written to %s", tokens, cost, options[:out])
