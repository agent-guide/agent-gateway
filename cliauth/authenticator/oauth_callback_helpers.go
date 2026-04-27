package authenticator

import (
	"fmt"
	"net/url"
	"strings"
)

func parseOAuthCallbackURL(provider, rawURL string, requireState bool) (code, state string, err error) {
	u, parseErr := url.Parse(strings.TrimSpace(rawURL))
	if parseErr != nil {
		return "", "", fmt.Errorf("%s: failed to parse callback URL: %w", provider, parseErr)
	}
	q := u.Query()
	if errParam := q.Get("error"); errParam != "" {
		return "", "", fmt.Errorf("%s: OAuth error from callback URL: %s", provider, errParam)
	}
	code = q.Get("code")
	if code == "" {
		return "", "", fmt.Errorf("%s: callback URL missing 'code' parameter", provider)
	}
	state = q.Get("state")
	if requireState && state == "" {
		return "", "", fmt.Errorf("%s: callback URL missing 'state' parameter", provider)
	}
	return code, state, nil
}

func buildOAuthSuccessHTML(iconHTML, gradientStart, gradientEnd string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Authentication Successful</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
               display: flex; justify-content: center; align-items: center;
               min-height: 100vh; margin: 0;
               background: linear-gradient(135deg, %s 0%%, %s 100%%); }
        .card { background: white; padding: 2.5rem; border-radius: 12px;
                box-shadow: 0 10px 25px rgba(0,0,0,0.1); max-width: 420px; text-align: center; }
        .icon { font-size: 3rem; }
        h1 { color: #1f2937; margin: 1rem 0 0.5rem; }
        p { color: #6b7280; }
        .countdown { margin-top: 1.5rem; color: #9ca3af; font-size: 0.85rem; }
    </style>
</head>
<body>
    <div class="card">
        <div class="icon">%s</div>
        <h1>Authentication Successful</h1>
        <p>You can close this window and return to your terminal.</p>
        <div class="countdown">Closing in <span id="t">10</span>s</div>
    </div>
    <script>
        let n = 10;
        const el = document.getElementById('t');
        const iv = setInterval(() => { el.textContent = --n; if (n <= 0) { clearInterval(iv); window.close(); } }, 1000);
    </script>
</body>
</html>`, gradientStart, gradientEnd, iconHTML)
}
