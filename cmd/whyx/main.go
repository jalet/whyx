// Command whyx explains why a rendered Helm value is what it is. It replays the
// layered value-file merge for one (target, chart) and shows each layer as a
// git-style diff, so the origin of any value reads like a commit history.
package main

import "github.com/jalet/whyx/internal/cli"

func main() {
	cli.Execute()
}
