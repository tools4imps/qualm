#!/usr/bin/env ruby
# frozen_string_literal: true

# Prints a spike run: files by verdict with their strongest answers, then each question's spread.
require "json"

run = JSON.parse(File.read(ARGV[0] || File.join(__dir__, "results.json")))
files = run["files"]
RELIEF = "mostly_boilerplate"
VERDICT = "hard_to_maintain"

puts format("%-34s %5s  %-7s %-6s  %s", "file", "lines", "verdict", "boiler", "strongest other answers")
files.sort_by { |f| -f["answers"][VERDICT] }.each do |f|
  top = f["answers"].reject { |id, _| [VERDICT, RELIEF].include?(id) }.sort_by { |_, v| -v }.first(4)
  puts format("%-34s %5d  %-7.2f %-6.2f  %s", f["path"].delete_prefix("lib/exhale/"), f["lines"], f["answers"][VERDICT],
              f["answers"][RELIEF], top.map { |id, v| format("%s %.2f", id, v) }.join(", "))
end

puts
puts format("%-22s %5s %5s %5s   %s", "question", "min", "med", "max", "highest file")
run["questions"].each do |id|
  values = files.map { |f| f["answers"][id] }.sort
  high = files.max_by { |f| f["answers"][id] }
  puts format("%-22s %5.2f %5.2f %5.2f   %s", id, values.first, values[values.size / 2], values.last,
              high["path"].delete_prefix("lib/exhale/"))
end
