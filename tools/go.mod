// Module tools exists only to pin the command-line tools the repo builds
// with, keeping their dependencies out of the app. Nothing imports it and
// it ships no code.
module github.com/pietjan/novamix/tools

go 1.27

tool (
	golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize
	golang.org/x/vuln/cmd/govulncheck
)

require (
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/telemetry v0.0.0-20261010193011-f71dc92da45e // indirect
	golang.org/x/tools v0.52.0 // indirect
	golang.org/x/vuln v1.9.0 // indirect
)
