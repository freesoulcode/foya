//go:build windows

package tool

import "github.com/freesoulcode/foya/internal/sandbox"

const windowsFileWriterScript = `$target = $args[0]
$directory = [System.IO.Path]::GetDirectoryName($target)
if ($directory) {
	[System.IO.Directory]::CreateDirectory($directory) | Out-Null
}
$output = [System.IO.File]::Create($target)
try {
	[Console]::OpenStandardInput().CopyTo($output)
} finally {
	$output.Dispose()
}`

const wslFileWriterScript = `set -eu
target=$1
case "$target" in
  */*) directory=${target%/*} ;;
  *) directory=. ;;
esac
/bin/mkdir -p -- "$directory"
umask 022
/bin/cat > "$target"
`

func fileWriterRequest(path string, kind sandbox.Kind, access sandbox.FSAccess) sandbox.ExecRequest {
	if kind == sandbox.KindWindowsWSL && access != sandbox.FSFull {
		return sandbox.ExecRequest{
			Argv:     []string{"/bin/sh", "-c", wslFileWriterScript, "foya-file-writer", path},
			PathArgs: []int{4},
		}
	}
	return sandbox.ExecRequest{
		Argv: []string{
			"powershell.exe",
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			windowsFileWriterScript,
			path,
		},
	}
}
