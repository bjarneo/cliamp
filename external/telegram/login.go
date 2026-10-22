package telegram

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bjarneo/cliamp/config"
	"github.com/gotd/td/session"
	tgclient "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// SignIn connects with cfg and runs the sign-in when the session is missing,
// storing it for the next runs. It prompts on stdin and stderr, so the caller
// must own the terminal: cliamp setup runs it after the wizard exits.
func SignIn(ctx context.Context, cfg config.TelegramConfig) error {
	if !cfg.IsSet() {
		return errors.New("telegram: api_id and api_hash are required")
	}
	if cfg.Phone == "" {
		return errors.New("telegram: phone is required to sign in")
	}
	path := sessionPath(cfg.SessionFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	client := tgclient.NewClient(cfg.AppID, cfg.AppHash, tgclient.Options{
		SessionStorage: &session.FileStorage{Path: path},
	})
	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("telegram: %w", err)
		}
		if status.Authorized {
			return nil
		}
		flow := auth.NewFlow(auth.Constant(normalizePhone(cfg.Phone), cfg.Password, promptCode()), auth.SendCodeOptions{})
		if err := flow.Run(ctx, client.Auth()); err != nil {
			return fmt.Errorf("telegram: sign in failed: %w", err)
		}
		return nil
	})
}

// normalizePhone keeps a leading "+" and the digits, so a number copied from
// an address book with spaces, dashes or parentheses still signs in.
func normalizePhone(phone string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(phone) {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && b.Len() == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// promptCode reads the login code from the terminal. Telegram sends it inside
// the app, not by SMS.
func promptCode() auth.CodeAuthenticator {
	return auth.CodeAuthenticatorFunc(func(_ context.Context, _ *tg.AuthSentCode) (string, error) {
		fmt.Fprint(os.Stderr, "Telegram login code: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(line), nil
	})
}

// sessionPath returns the session file for sessionFile, expanding a leading
// "~/" and defaulting to ~/.local/share/cliamp/telegram.session.
func sessionPath(sessionFile string) string {
	path := sessionFile
	if path == "" {
		path = "~/.local/share/cliamp/telegram.session"
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	return path
}
