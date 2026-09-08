// ghostchrome is the CLI browser automation binary.
package main

import (
	"os"

	"github.com/dev-toolings/ghostchrome"
	"github.com/dev-toolings/ghostchrome/internal/surface/cli"
)

var version = "dev"

func init() {
	cli.SetVersion(version)
	cli.SetEmbeddedSkill("ghostchrome", ghostchrome.SkillMarkdown)
	for relative, content := range ghostchrome.SkillFiles() {
		cli.SetEmbeddedSkillFile(relative, content)
	}
}

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
