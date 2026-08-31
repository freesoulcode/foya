//go:build !windows

package tool

import "github.com/freesoulcode/foya/internal/sandbox"

const fileWriterScript = `set -eu
target=$1
case "$target" in
  */*) directory=${target%/*} ;;
  *) directory=. ;;
esac
/bin/mkdir -p -- "$directory"
umask 022
/bin/cat > "$target"
`

func fileWriterRequest(path string, _ sandbox.Kind, _ sandbox.FSAccess) sandbox.ExecRequest {
	return sandbox.ExecRequest{
		Argv:     []string{"/bin/sh", "-c", fileWriterScript, "foya-file-writer", path},
		PathArgs: []int{4},
	}
}
