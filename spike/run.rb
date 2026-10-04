#!/usr/bin/env ruby
# frozen_string_literal: true

# A spike, to be thrown away: ask Jev qualm's draft questions about each file and save what it says.
#
#   OPENROUTER_API_KEY=... ruby run.rb [options] FILE...
#     --out FILE      where the answers go (default results.json, next to this script)
#     --only a,b      ask only these question ids
#     --fresh         skip the cache, to see how much the answers move between calls
#     --root DIR      print paths relative to DIR
require "optparse"
require_relative "jev"

options = { out: File.join(__dir__, "results.json"), only: nil, fresh: false, root: Dir.pwd }
files = OptionParser.new do |o|
  o.on("--out FILE") { |v| options[:out] = v }
  o.on("--only IDS") { |v| options[:only] = v.split(",") }
  o.on("--fresh") { options[:fresh] = true }
  o.on("--root DIR") { |v| options[:root] = v }
end.parse(ARGV)
abort "Which files?" if files.empty?

questions = Spike.questions(options[:only])
results = Spike.pool(files) do |path|
  text = File.read(path, encoding: "UTF-8").scrub
  Spike.judge(text, language: Spike.language_for(path), questions: questions, fresh: options[:fresh])
       .merge("path" => path.delete_prefix("#{options[:root]}/"), "bytes" => text.bytesize, "lines" => text.count("\n"))
end.sort_by { |r| r["path"] }

File.write(options[:out], JSON.pretty_generate({ "model" => Spike::MODEL, "questions" => questions.map { |q| q["id"] },
                                                 "files" => results }))
warn format("%d files, %d questions each, %d input tokens billed, $%.5f. Answers in %s", results.size, questions.size,
            results.sum { |r| r["tokens"] }, results.sum { |r| r["cost"] }, options[:out])
