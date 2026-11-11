package engine

import (
	"os"
	"os/exec"
)

// NodeCommand builds the production engine process: Node running the engine
// program, with an environment of PATH only, so no provider key or secret of
// the operator reaches it, and nothing secret on its command line. Its
// credentials arrive over stdin when it asks for them.
func NodeCommand(node, entry string) func() *exec.Cmd {
	return func() *exec.Cmd {
		cmd := exec.Command(node, entry)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		return cmd
	}
}
