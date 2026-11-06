// Module tools exists only to pin the command-line tools the repo builds
// with, keeping their dependencies out of the app. Nothing imports it and
// it ships no code.
module github.com/pietjan/novamix/tools

go 1.27

tool (
	github.com/tc-hib/go-winres
	golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize
	golang.org/x/vuln/cmd/govulncheck
)

require (
	github.com/cpuguy83/go-md2man/v2 v2.0.2 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/tc-hib/go-winres v0.3.3 // indirect
	github.com/tc-hib/winres v0.2.1 // indirect
	github.com/urfave/cli/v2 v2.25.7 // indirect
	github.com/xrash/smetrics v0.0.0-20201216005158-039620a65673 // indirect
	golang.org/x/image v0.12.0 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/telemetry v0.0.0-20261010193011-f71dc92da45e // indirect
	golang.org/x/tools v0.52.0 // indirect
	golang.org/x/vuln v1.9.0 // indirect
)
