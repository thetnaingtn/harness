package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateURLNotInternal_BlocksLoopback(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/",
		"http://localhost/",
		"http://[::1]/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/",   // AWS/GCP/Azure metadata IP
		"http://metadata.google.internal/", // GCP metadata host
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			err := ValidateURLNotInternal(u)
			assert.Error(t, err, "url %s should be blocked as internal", u)
		})
	}
}

func TestValidateURLNotInternal_AllowsPublic(t *testing.T) {
	for _, u := range []string{
		"https://example.com/",
		"https://api.github.com/repos/foo/bar",
		"http://1.1.1.1/",
	} {
		err := ValidateURLNotInternal(u)
		assert.NoError(t, err, "public url %s should be allowed", u)
	}
}

func TestValidateURLNotInternal_RejectsBadScheme(t *testing.T) {
	// file:// URLs lack a host, so SSRF guard rejects on parse — either
	// "no host" or a scheme/http complaint is acceptable; what matters is
	// it does not silently allow the URL.
	err := ValidateURLNotInternal("file:///etc/passwd")
	assert.Error(t, err, "non-http schemes must be rejected")
	_ = strings.TrimSpace(err.Error())
}
