package cli

// Version is the release this source builds. It has a file to itself because the release workflow
// watches this path: a new number landing on main is what publishes a release. The gem takes its
// version from here too.
const Version = "0.1.1"
