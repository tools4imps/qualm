# frozen_string_literal: true

# qualm is a Go program: https://github.com/tools4imps/qualm. This gem carries its binary for one
# platform. The `qualm` command runs it, and Qualm.binary gives its path to anything that would
# sooner run it directly.
module Qualm
  # The Go binary inside the installed gem.
  def self.binary
    File.expand_path("../libexec/qualm#{".exe" if Gem.win_platform?}", __dir__)
  end
end
