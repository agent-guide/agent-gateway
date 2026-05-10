package authenticator

import (
	"fmt"
	"strings"
	"time"

	"github.com/agent-guide/agent-gateway/pkg/cliauth"
)

type oauthBrowserFlowOptions struct {
	ProviderName              string
	AuthURL                   string
	NoBrowser                 bool
	Reporter                  cliauth.LoginStatusReporter
	AwaitingBrowserMessage    string
	WaitingForCallbackMessage string
	ManualCallbackMessage     string
	ManualCallbackPrompt      string
	CallbackTimeout           time.Duration
	ManualPromptDelay         time.Duration
	WaitForCallback           func(time.Duration) (code, state string, err error)
	ParseCallbackURL          func(string) (code, state string, err error)
}

type oauthBrowserFlowResult struct {
	Code  string
	State string
}

func runOAuthBrowserFlow(opts oauthBrowserFlowOptions) (oauthBrowserFlowResult, error) {
	reportLoginStatus(opts.Reporter, cliauth.LoginStatusUpdate{
		Phase:           "awaiting_browser_auth",
		Message:         opts.AwaitingBrowserMessage,
		VerificationURL: opts.AuthURL,
	})

	if opts.NoBrowser {
		fmt.Printf("Visit the following URL to authenticate with %s:\n%s\n", opts.ProviderName, opts.AuthURL)
	} else {
		fmt.Printf("Opening browser for %s authentication...\n", opts.ProviderName)
		if openErr := openBrowser(opts.AuthURL); openErr != nil {
			fmt.Printf("Could not open browser automatically. Please visit:\n%s\n", opts.AuthURL)
		}
	}

	fmt.Printf("Waiting for %s authentication callback...\n", opts.ProviderName)
	reportLoginStatus(opts.Reporter, cliauth.LoginStatusUpdate{
		Phase:           "waiting_for_callback",
		Message:         opts.WaitingForCallbackMessage,
		VerificationURL: opts.AuthURL,
	})

	cbCh := make(chan oauthBrowserFlowResult, 1)
	cbErrCh := make(chan error, 1)
	go func() {
		code, state, waitErr := opts.WaitForCallback(opts.CallbackTimeout)
		if waitErr != nil {
			cbErrCh <- waitErr
			return
		}
		cbCh <- oauthBrowserFlowResult{Code: code, State: state}
	}()

	manualTimer := time.NewTimer(opts.ManualPromptDelay)
	defer manualTimer.Stop()
	var manualLineCh <-chan string
	var manualLineErrCh <-chan error

	for {
		select {
		case outcome := <-cbCh:
			return outcome, nil
		case waitErr := <-cbErrCh:
			return oauthBrowserFlowResult{}, newAuthError(ErrCallbackTimeout, waitErr)
		case <-manualTimer.C:
			if opts.ManualCallbackMessage != "" {
				reportLoginStatus(opts.Reporter, cliauth.LoginStatusUpdate{
					Phase:           "waiting_for_manual_callback",
					Message:         opts.ManualCallbackMessage,
					VerificationURL: opts.AuthURL,
				})
			}
			select {
			case outcome := <-cbCh:
				return outcome, nil
			case waitErr := <-cbErrCh:
				return oauthBrowserFlowResult{}, newAuthError(ErrCallbackTimeout, waitErr)
			default:
			}
			manualLineCh, manualLineErrCh = asyncReadLine(opts.ManualCallbackPrompt)
		case line := <-manualLineCh:
			manualLineCh = nil
			manualLineErrCh = nil
			if strings.TrimSpace(line) == "" {
				manualLineCh, manualLineErrCh = asyncReadLine(opts.ManualCallbackPrompt)
				continue
			}
			code, state, parseErr := opts.ParseCallbackURL(line)
			if parseErr != nil {
				return oauthBrowserFlowResult{}, parseErr
			}
			return oauthBrowserFlowResult{Code: code, State: state}, nil
		case readErr := <-manualLineErrCh:
			return oauthBrowserFlowResult{}, readErr
		}
	}
}
