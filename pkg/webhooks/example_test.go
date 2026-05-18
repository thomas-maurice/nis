package webhooks_test

import (
	"net/http"

	"github.com/thomas-maurice/nis/pkg/webhooks"
)

func ExampleVerify() {
	secret := []byte("shared-with-nis")
	http.HandleFunc("/webhooks/nis", func(w http.ResponseWriter, r *http.Request) {
		body, err := webhooks.Verify(r, secret, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		// body is the original POST body, ready to json.Decode.
		defer body.Close() //nolint:errcheck
		_ = body
		w.WriteHeader(http.StatusOK)
	})
}
