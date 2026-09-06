//go:build windows

package tool

import (
	"fmt"
	"os/exec"
	"strings"
)

const windowsTrashScript = `Add-Type -AssemblyName Microsoft.VisualBasic
$item = Get-Item -LiteralPath $args[0] -Force
if ($item.PSIsContainer) {
	[Microsoft.VisualBasic.FileIO.FileSystem]::DeleteDirectory(
		$item.FullName,
		[Microsoft.VisualBasic.FileIO.UIOption]::OnlyErrorDialogs,
		[Microsoft.VisualBasic.FileIO.RecycleOption]::SendToRecycleBin)
} else {
	[Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile(
		$item.FullName,
		[Microsoft.VisualBasic.FileIO.UIOption]::OnlyErrorDialogs,
		[Microsoft.VisualBasic.FileIO.RecycleOption]::SendToRecycleBin)
}`

func moveToTrash(path string) error {
	output, err := exec.Command(
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		windowsTrashScript,
		path,
	).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}
