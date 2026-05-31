package main

// Commit is injected at build time with -ldflags. The development fallback is
// intentionally explicit so update checks can fall back to config/repo state.
var Commit = "dev"
