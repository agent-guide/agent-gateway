package adminclient

import "net/url"

func withQuery(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}
