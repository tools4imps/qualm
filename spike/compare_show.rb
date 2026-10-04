#!/usr/bin/env ruby
# frozen_string_literal: true

# Reads a compare run and prints how the change-shaped questions behaved.
require "json"

run = JSON.parse(File.read(ARGV[0] || File.join(__dir__, "compare.json")))
short = ->(path) { path.sub(%r{\Alib/[^/]+/}, "") }
rows = run["commits"].flat_map { |c| c["changes"].map { |ch| ch.merge("commit" => c) } }
both = rows.select { |r| r["pair"] && r["diff"] }
puts "#{run["commits"].size} commits, #{rows.size} modified files, #{both.size} asked both ways"

%w[pair diff].each do |way|
  puts "\nBy commit type, asked as #{way}"
  puts format("%-9s %5s %7s %6s %7s %10s %12s", "type", "files", "harder", "same", "easier", "push_back", "push_back>.5")
  rows.select { |r| r[way] }.group_by { |r| r["commit"]["type"] }.sort_by { |_, rs| -rs.size }.each do |type, rs|
    n = rs.size.to_f
    m = ->(k) { rs.sum { |r| r[way]["direction"][k] } / n }
    puts format("%-9s %5d %7.2f %6.2f %7.2f %10.2f %11.0f%%", type, rs.size, m.("harder"), m.("same"), m.("easier"),
                rs.sum { |r| r[way]["push_back"] } / n, 100 * rs.count { |r| r[way]["push_back"] > 0.5 } / n)
  end
end

show = lambda do |r, way|
  a = r[way]
  d = a["direction"]
  format("%-4s harder %.2f same %.2f easier %.2f | push_back %.2f simplified %.2f new %.2f tangle %.2f big_unit %.2f", way,
         d["harder"], d["same"], d["easier"], a["push_back"], a["simplified"], a["new_behaviour"], a["added_tangle"], a["grew_a_big_unit"])
end
cases = { "9ccf424" => "the real refactor: daemon orchestration split out of Runner",
          "f5f0a1e" => "comments converted to YARD docstrings (first three files)",
          "0267311" => "a rename of the whole gem (first two files)",
          "5dc5915" => "stubs becoming fork-isolated execution",
          "fc68635" => "a stub becoming the coverage map" }
cases.each do |sha, what|
  commit = run["commits"].find { |c| c["sha"] == sha } or next
  puts "\n#{sha}: #{what}"
  commit["changes"].first(sha == "9ccf424" || sha == "5dc5915" ? 4 : 3).first(sha == "0267311" ? 2 : 4).each do |ch|
    puts format("  %-26s %4d -> %-4d lines", short.call(ch["path"])[0, 26], *ch["lines"])
    %w[pair diff].each { |way| puts "     #{show.call(ch, way)}" if ch[way] }
  end
end

puts "\nThe file that crept: coverage_map.rb, step by step (asked as diff)"
rows.select { |r| r["path"].end_with?("coverage_map.rb") && r["diff"] }.each do |r|
  a = r["diff"]
  puts format("  %s %-8s %4d -> %-4d harder %.2f push_back %.2f big_unit %.2f tangle %.2f  %s", r["commit"]["sha"], r["commit"]["type"], *r["lines"],
              a["direction"]["harder"], a["push_back"], a["grew_a_big_unit"], a["added_tangle"], r["commit"]["subject"][0, 44])
end

puts "\nWhat a gate on push_back would block"
[0.5, 0.6, 0.7].each do |bar|
  %w[pair diff].each do |way|
    hit = run["commits"].select { |c| c["changes"].any? { |ch| ch[way] && ch[way]["push_back"] > bar } }
    puts format("  %s push_back > %.1f: %2d of %d commits (%s)", way, bar, hit.size, run["commits"].size,
                hit.group_by { |c| c["type"] }.map { |t, cs| "#{cs.size} #{t}" }.join(", "))
  end
end

puts "\nStrongest push_back (asked as diff)"
rows.select { |r| r["diff"] }.sort_by { |r| -r["diff"]["push_back"] }.first(10).each do |r|
  a = r["diff"]
  puts format("  %.2f %-22s %4d -> %-4d %s %-50s harder %.2f big_unit %.2f tangle %.2f copies %.2f", a["push_back"], short.call(r["path"])[0, 22], *r["lines"],
              r["commit"]["sha"], r["commit"]["subject"][0, 50], a["direction"]["harder"], a["grew_a_big_unit"], a["added_tangle"], a["added_copies"])
end

corr = lambda do |xs, ys|
  n = xs.size.to_f
  mx = xs.sum / n
  my = ys.sum / n
  xs.zip(ys).sum { |x, y| (x - mx) * (y - my) } / Math.sqrt(xs.sum { |x| (x - mx)**2 } * ys.sum { |y| (y - my)**2 })
end
puts "\nDo the two ways of asking agree?"
puts format("  correlation of push_back, pair against diff: %.2f", corr.call(both.map { |r| r["pair"]["push_back"] }, both.map { |r| r["diff"]["push_back"] }))
puts format("  correlation of harder: %.2f", corr.call(both.map { |r| r["pair"]["direction"]["harder"] }, both.map { |r| r["diff"]["direction"]["harder"] }))
same_call = both.count { |r| r["pair"]["direction"]["choice"] == r["diff"]["direction"]["choice"] }
puts format("  same direction chosen: %d of %d (%.0f%%)", same_call, both.size, 100.0 * same_call / both.size)
puts format("  push_back against lines added (diff): %.2f", corr.call(rows.select { |r| r["diff"] }.map { |r| (r["lines"][1] - r["lines"][0]).to_f },
                                                                        rows.select { |r| r["diff"] }.map { |r| r["diff"]["push_back"] }))

puts "\nHow often each diagnosis fires above 0.5 (asked as diff), and its strongest case"
%w[simplified comments_only new_behaviour added_tangle grew_a_big_unit added_copies added_impossible_guards added_placeholders added_unused_flexibility].each do |q|
  rs = rows.select { |r| r["diff"] }
  top = rs.max_by { |r| r["diff"][q] }
  puts format("  %-26s %3d of %d   strongest %.2f %s %s", q, rs.count { |r| r["diff"][q] > 0.5 }, rs.size, top["diff"][q], top["commit"]["sha"], short.call(top["path"]))
end
