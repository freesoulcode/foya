package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/sandbox"
)

func executionProfile(ctx context.Context) sandbox.Profile {
	if interaction.ModeFromContext(ctx) == interaction.ModeFullAccess {
		return sandbox.Profile{FileSystem: sandbox.FSFull, Network: true}
	}
	return sandbox.WorkspaceWriteProfile(CWDFromContext(ctx))
}

func writeFileAtBoundary(
	ctx context.Context,
	runner sandbox.Runner,
	path string,
	content []byte,
) error {
	if runner == nil {
		return errors.New("execution boundary is unavailable")
	}
	profile := executionProfile(ctx)
	request := fileWriterRequest(path, runner.Kind(), profile.FileSystem)
	request.Dir = CWDFromContext(ctx)
	request.Stdin = content
	result, err := runner.Run(ctx, request, profile)
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(result.Stderr))
	if detail == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, detail)
}
