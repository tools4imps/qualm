#!/usr/bin/env ruby
# frozen_string_literal: true

# A spike: walk a repository's mainline and score every changed file before and after each commit,
# to see whether the answers rise and fall where the code really got worse or better.
#
#   OPENROUTER_API_KEY=... ruby history.rb REPO [--rev origin/main] [--paths 'lib/*.rb,lib/**/*.rb']
#                                               [--out history.json] [--limit N]
#
# A mainline commit is compared with its first parent, which is how qualm would see a pull request.
require "open3"
require "optparse"
require_relative "jev"

options = { rev: "origin/main", paths: ["lib/*.rb", "lib/**/*.rb"], out: File.join(__dir__, "history.json"), limit: nil }
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

log = git.call("log", "--first-parent", "--reverse", "--format=%H%x09%ad%x09%s", "--date=short", options[:rev],
               "--", *options[:paths]).lines(chomp: true)
log = log.last(options[:limit]) if options[:limit]

commits = log.filter_map do |line|
  sha, date, subject = line.split("\t", 3)
  next unless git.call("rev-parse", "--verify", "--quiet", "#{sha}^1")

  changes = git.call("diff", "--name-status", "-M", "#{sha}^1", sha, "--", *options[:paths]).lines(chomp: true).map do |row|
    status, old, new = row.split("\t")
    new ||= old
    { "status" => status[0], "path" => new, "was" => (old unless status[0] == "A"),
      "before" => (git.call("show", "#{sha}^1:#{old}") unless status[0] == "A"),
      "after" => (git.call("show", "#{sha}:#{new}") unless status[0] == "D") }
  end
  type = subject[/\A([a-z]+)(\([^)]*\))?!?:/, 1] || (subject.start_with?("Merge") ? "merge" : "other")
  { "sha" => sha[0, 7], "date" => date, "type" => type, "subject" => subject, "changes" => changes }
end

# Each distinct version of a file is judged once, however many commits it sits between.
versions = {}
commits.each do |c|
  c["changes"].each do |ch|
    %w[before after].each do |side|
      text = ch[side] or next
      versions[Digest::SHA256.hexdigest(text)] ||= [text, Spike.language_for(ch["path"])]
    end
  end
end
warn "#{commits.size} commits, #{commits.sum { |c| c["changes"].size }} file changes, #{versions.size} distinct versions to judge"

questions = Spike.questions
done = 0
lock = Mutex.new
judged = Spike.pool(versions.to_a, workers: 8) do |digest, (text, language)|
  result = Spike.judge(text, language: language, questions: questions)
  lock.synchronize { done += 1; warn "  #{done}/#{versions.size}" if (done % 50).zero? }
  [digest, result.merge("lines" => text.count("\n"))]
end.to_h

commits.each do |c|
  c["changes"].each do |ch|
    %w[before after].each do |side|
      text = ch.delete(side)
      ch[side] = text && judged.fetch(Digest::SHA256.hexdigest(text)).slice("answers", "lines")
    end
  end
end

File.write(options[:out], JSON.pretty_generate({ "repo" => repo, "rev" => options[:rev], "model" => Spike::MODEL,
                                                 "questions" => questions.map { |q| q["id"] }, "commits" => commits }))
warn format("%d input tokens billed, $%.4f. Written to %s", judged.values.sum { |r| r["tokens"] },
            judged.values.sum { |r| r["cost"] }, options[:out])
