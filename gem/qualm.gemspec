# frozen_string_literal: true

# qualm is a Go program. This gem carries its binary for one platform, with a small Ruby command
# that hands the command line to it.
#
# `rake release:build` stages a copy of this folder for each platform, drops that platform's
# binary into libexec/, and builds the gem there. The version and the platform come from the
# Rakefile, which reads the version out of internal/cli/version.go.

Gem::Specification.new do |spec|
  spec.name = "qualm"
  spec.version = ENV.fetch("QUALM_VERSION")
  spec.platform = Gem::Platform.new(ENV.fetch("QUALM_PLATFORM"))
  spec.authors = ["Obie Fernandez"]
  spec.email = ["obiefernandez@gmail.com"]

  spec.summary = "The subjective gate for Impatient Programming: no PR merges while a reviewer would ask for the change to be simplified"
  spec.description = <<~DESC.strip.gsub(/\n/, " ")
    qualm fails a pull request when an experienced reviewer would ask for the change to be
    simplified, and it tells the coding agent what to fix. It asks Jev, TypeSafe's System One
    decision model, about each changed file's diff. qualm is one Go binary with no parser, so it
    judges code in any language. This gem installs that binary for your platform.
  DESC
  spec.homepage = "https://github.com/tools4imps/qualm"
  spec.license = "MIT"
  spec.required_ruby_version = ">= 3.1"

  spec.metadata["source_code_uri"] = spec.homepage
  spec.metadata["changelog_uri"] = "#{spec.homepage}/blob/main/CHANGELOG.md"
  spec.metadata["rubygems_mfa_required"] = "true"

  spec.files = Dir["exe/*", "lib/**/*.rb", "libexec/*", "README.md", "LICENSE"]
  spec.bindir = "exe"
  spec.executables = ["qualm"]
  spec.require_paths = ["lib"]
end
