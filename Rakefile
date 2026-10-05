# frozen_string_literal: true

# Release tooling. qualm is a Go program, and Ruby is here for one job: putting its binary on
# RubyGems. The tasks have the shape of exhale-ruby's, and the release workflow drives them in the
# same order: build, status, push.
#
# internal/cli/version.go is the one source of the version. Nothing here hardcodes it.

require "digest"
require "fileutils"
require "json"
require "open-uri"

NAME = "qualm"
VERSION = File.read("internal/cli/version.go")[/^const Version = "(\d+\.\d+\.\d+)"$/, 1] or
          abort "release: no version found in internal/cli/version.go"

# What Go calls each platform, then what RubyGems calls it.
TARGETS = [
  %w[darwin arm64 arm64-darwin],
  %w[darwin amd64 x86_64-darwin],
  %w[linux arm64 aarch64-linux],
  %w[linux amd64 x86_64-linux],
  %w[windows arm64 arm64-mingw-ucrt],
  %w[windows amd64 x64-mingw-ucrt]
].freeze

def binary(os, arch) = "dist/#{os}_#{arch}/qualm#{".exe" if os == "windows"}"
def gem_file(platform) = "pkg/#{NAME}-#{VERSION}-#{platform}.gem"

# The platforms of this version that RubyGems already holds.
def published
  body = URI.open("https://rubygems.org/api/v1/versions/#{NAME}.json", &:read)
  JSON.parse(body).select { |v| v["number"] == VERSION }.map { |v| v["platform"] }
rescue OpenURI::HTTPError
  [] # 404: the gem has never been published
end

namespace :release do
  desc "Print the version in internal/cli/version.go"
  task :version do
    puts VERSION
  end

  desc "Cross-compile the binary for every platform into dist/"
  task :binaries do
    FileUtils.rm_rf("dist")
    TARGETS.each do |os, arch, _|
      # CGO off gives a static binary, so one Linux build runs on glibc and musl alike.
      env = { "GOOS" => os, "GOARCH" => arch, "CGO_ENABLED" => "0" }
      sh env, "go", "build", "-trimpath", "-ldflags", "-s -w", "-o", binary(os, arch), "./cmd/qualm"
    end
  end

  desc "Build a gem and an archive for every platform into pkg/, with --strict. " \
       "Reversible, since pkg/ is gitignored. release:push is the half that can't be undone."
  task build: :binaries do
    FileUtils.rm_rf("pkg")
    TARGETS.each do |os, arch, platform|
      stage = "pkg/stage/#{platform}"
      FileUtils.mkdir_p("#{stage}/libexec")
      FileUtils.cp_r(Dir["gem/*"], stage, preserve: true)
      FileUtils.cp(%w[README.md LICENSE], stage)
      FileUtils.cp(binary(os, arch), "#{stage}/libexec", preserve: true)

      # --strict turns any gemspec warning into a build failure.
      env = { "QUALM_VERSION" => VERSION, "QUALM_PLATFORM" => platform }
      sh env, "gem", "build", "qualm.gemspec", "--strict", "--output", File.expand_path(gem_file(platform)),
         chdir: stage

      # The same binary goes on the GitHub release, for anyone without Ruby.
      archive = File.expand_path("pkg/#{NAME}_#{VERSION}_#{os}_#{arch}")
      files = ["LICENSE", "README.md", "libexec/#{File.basename(binary(os, arch))}"]
      if os == "windows"
        sh "zip", "-q", "-j", "#{archive}.zip", *files, chdir: stage
      else
        sh "tar", "-czf", "#{archive}.tar.gz", "-C", stage, "LICENSE", "README.md", "-C", "libexec", "qualm"
      end
    end
    FileUtils.rm_rf("pkg/stage")

    sums = Dir["pkg/*.{tar.gz,zip}"].sort.map { |f| "#{Digest::SHA256.file(f).hexdigest}  #{File.basename(f)}" }
    File.write("pkg/checksums.txt", sums.join("\n") + "\n")
  end

  desc "Print publish=true when a platform of this version is not yet on RubyGems, and " \
       "publish=false when all of them are. The release workflow reads this to decide " \
       "whether to fetch credentials at all."
  task :status do
    missing = TARGETS.map(&:last) - published
    puts "publish=#{missing.any?}"
  end

  desc "Push each pkg/qualm-<version>-<platform>.gem to RubyGems. A platform already there is " \
       "skipped, so a re-run after a partial push is safe. The release workflow runs this with a " \
       "short-lived key from trusted publishing. By hand it needs RubyGems MFA."
  task :push do
    there = published
    TARGETS.each do |_, _, platform|
      file = gem_file(platform)
      abort "release:push: #{file} is missing, run `rake release:build` first" unless File.exist?(file)

      if there.include?(platform)
        puts "skip  #{NAME} #{VERSION} #{platform} (already on RubyGems)"
        next
      end

      puts "push  #{file}"
      sh "gem", "push", file
    end
  end
end
