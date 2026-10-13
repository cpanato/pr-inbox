package auth

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func Token(parent context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com")
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("GitHub CLI authentication timed out")
		}
		if _, ok := err.(*exec.Error); ok {
			return "", fmt.Errorf("GitHub CLI (`gh`) is required; install it and run `gh auth login`")
		}
		return "", fmt.Errorf("could not read GitHub CLI credentials; run `gh auth login`")
	}
	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", fmt.Errorf("GitHub CLI returned an empty credential; run `gh auth login`")
	}
	return token, nil
}
