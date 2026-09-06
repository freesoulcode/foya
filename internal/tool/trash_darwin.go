//go:build darwin

package tool

import (
	"fmt"
	"os/exec"
	"strings"
)

const macOSTrashScript = `ObjC.import("Foundation");
function run(argv) {
	const url = $.NSURL.fileURLWithPath($(argv[0]).stringByStandardizingPath);
	const result = Ref();
	const error = Ref();
	const ok = $.NSFileManager.defaultManager.trashItemAtURLResultingItemURLError(url, result, error);
	if (!ok) throw new Error(ObjC.unwrap(error[0].localizedDescription));
}`

func moveToTrash(path string) error {
	output, err := exec.Command(
		"/usr/bin/osascript",
		"-l",
		"JavaScript",
		"-e",
		macOSTrashScript,
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
