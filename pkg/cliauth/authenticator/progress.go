package authenticator

import "github.com/agent-guide/agent-gateway/pkg/cliauth"

func reportLoginStatus(reporter cliauth.LoginStatusReporter, update cliauth.LoginStatusUpdate) {
	if reporter == nil {
		return
	}
	reporter.UpdateLoginStatus(update)
}
