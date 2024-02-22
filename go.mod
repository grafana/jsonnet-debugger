module github.com/grafana/jsonnet-debugger

go 1.21.6

require (
	github.com/google/go-dap v0.11.0
	github.com/google/go-jsonnet v0.20.0
	github.com/gookit/color v1.5.4
	github.com/lmittmann/tint v1.0.4
	github.com/peterh/liner v1.2.2
)

replace github.com/google/go-jsonnet => github.com/grafana/go-jsonnet-debugger v0.0.0-20231228080326-875660bf944e

require (
	github.com/mattn/go-runewidth v0.0.3 // indirect
	github.com/xo/terminfo v0.0.0-20210125001918-ca9a967f8778 // indirect
	golang.org/x/crypto v0.9.0 // indirect
	golang.org/x/sys v0.10.0 // indirect
	gopkg.in/yaml.v2 v2.2.7 // indirect
	sigs.k8s.io/yaml v1.1.0 // indirect
)
