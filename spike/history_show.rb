#!/usr/bin/env ruby
# frozen_string_literal: true

# Reads a history run and prints: how the verdict moved by commit type, the biggest declines and
# improvements, what a decline gate would have blocked, and how much of it is only file size.
require "json"

run = JSON.parse(File.read(ARGV[0] || File.join(__dir__, "history.json")))
V = "hard_to_maintain"
short = ->(path) { path.sub(%r{\Alib/[^/]+/}, "") }

rows = run["commits"].flat_map do |c|
  c["changes"].map do |ch|
    b, a = ch["before"], ch["after"]
    { commit: c, status: ch["status"], path: ch["path"], before: b, after: a,
      delta: (a["answers"][V] - b["answers"][V] if a && b), lines: (a["lines"] - b["lines"] if a && b) }
  end
end
changed = rows.select { |r| r[:delta] }
added = rows.select { |r| r[:status] == "A" && r[:after] }
puts "#{run["commits"].size} commits, #{rows.size} file changes: #{changed.size} modified, #{added.size} added"

puts "\nVerdict movement per modified file, by commit type"
puts format("%-9s %5s %8s %8s %7s %7s %7s", "type", "files", "mean", "lines", "up>.05", "dn>.05", "flat")
changed.group_by { |r| r[:commit]["type"] }.sort_by { |_, rs| -rs.size }.each do |type, rs|
  n = rs.size.to_f
  puts format("%-9s %5d %+8.3f %+8.1f %6.0f%% %6.0f%% %6.0f%%", type, rs.size, rs.sum { |r| r[:delta] } / n,
              rs.sum { |r| r[:lines] } / n, 100 * rs.count { |r| r[:delta] > 0.05 } / n,
              100 * rs.count { |r| r[:delta] < -0.05 } / n, 100 * rs.count { |r| r[:delta].abs <= 0.05 } / n)
end

movers = lambda do |r|
  r[:after]["answers"].keys.reject { |q| q == V }
                      .map { |q| [q, r[:after]["answers"][q] - r[:before]["answers"][q]] }
                      .sort_by { |_, d| -d.abs }.first(2).map { |q, d| format("%s %+.2f", q, d) }.join(", ")
end
line = lambda do |r|
  format("%+.2f  %.2f->%.2f  %4d->%-4d %-24s %s %s | %s", r[:delta], r[:before]["answers"][V], r[:after]["answers"][V],
         r[:before]["lines"], r[:after]["lines"], short.call(r[:path])[0, 24], r[:commit]["sha"], r[:commit]["subject"][0, 46], movers.call(r))
end
puts "\nBiggest declines (verdict rose)"
changed.sort_by { |r| -r[:delta] }.first(12).each { |r| puts line.call(r) }
puts "\nBiggest improvements (verdict fell)"
changed.sort_by { |r| r[:delta] }.first(10).each { |r| puts line.call(r) }

puts "\nNew files arriving with the strongest verdict"
added.sort_by { |r| -r[:after]["answers"][V] }.first(8).each do |r|
  puts format("%.2f  %4d lines  %-26s %s %s", r[:after]["answers"][V], r[:after]["lines"], short.call(r[:path])[0, 26],
              r[:commit]["sha"], r[:commit]["subject"][0, 56])
end

puts "\nWhat a decline gate would have blocked (commits with at least one file over the margin)"
total = run["commits"].size
[0.05, 0.10, 0.15, 0.20].each do |margin|
  hit = run["commits"].count { |c| rows.any? { |r| r[:commit].equal?(c) && r[:delta] && r[:delta] > margin } }
  puts format("  margin %.2f: %2d of %d commits (%.0f%%)", margin, hit, total, 100.0 * hit / total)
end
[0.7, 0.8].each do |bar|
  hit = run["commits"].count { |c| added.any? { |r| r[:commit].equal?(c) && r[:after]["answers"][V] >= bar } }
  puts format("  new file at %.1f or above: %2d of %d commits", bar, hit, total)
end

corr = lambda do |xs, ys|
  n = xs.size.to_f
  mx = xs.sum / n
  my = ys.sum / n
  cov = xs.zip(ys).sum { |x, y| (x - mx) * (y - my) }
  cov / Math.sqrt(xs.sum { |x| (x - mx)**2 } * ys.sum { |y| (y - my)**2 })
end
latest = {}
rows.each { |r| latest[r[:path]] = r[:after] if r[:after] }
puts format("\nHow much is size? correlation of the verdict with log(lines) across %d files: %.2f", latest.size,
            corr.call(latest.values.map { |a| Math.log(a["lines"] + 1) }, latest.values.map { |a| a["answers"][V] }))
puts format("correlation of the change in verdict with the change in lines, per modified file: %.2f",
            corr.call(changed.map { |r| r[:lines].to_f }, changed.map { |r| r[:delta] }))

puts "\nThe four largest files over time (verdict at first sight, at its peak, now)"
latest.sort_by { |_, a| -a["lines"] }.first(4).each do |path, now|
  seen = rows.select { |r| r[:path] == path && r[:after] }
  first = seen.first[:after]
  peak = seen.max_by { |r| r[:after]["answers"][V] }[:after]
  puts format("  %-22s %4d lines %.2f  ->  peak %.2f at %d lines  ->  now %.2f at %d lines (%d changes)", short.call(path), first["lines"],
              first["answers"][V], peak["answers"][V], peak["lines"], now["answers"][V], now["lines"], seen.size)
end
