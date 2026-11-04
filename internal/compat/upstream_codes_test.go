package compat

import (
	"errors"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

// Each code the upstream translation can produce is answered with its status,
// its kind and its sentence together. Where the sentence would have been the
// provider's own words, the caller gets the server's sentence instead.
func TestTranslateUpstreamAnswersEachCodeWithItsOwnStatusAndSentence(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		status  int
		kind    string
		code    string
		message string
	}{
		{
			"rate limit",
			&adapter.Error{Kind: adapter.ErrorRateLimit, Message: "slow down, account 123"},
			http.StatusTooManyRequests, "rate_limit_error", "provider_rate_limited",
			"The provider is rate limiting this server. Try again shortly.",
		},
		{
			"refusal",
			&adapter.Error{Kind: adapter.ErrorRefusal, Message: "policy text from model x"},
			http.StatusBadRequest, "invalid_request_error", "refusal",
			"The model declined to answer this request.",
		},
		{
			"images unsupported",
			&adapter.Error{Kind: adapter.ErrorImagesUnsupported, Message: "model x takes no images"},
			http.StatusBadRequest, "invalid_request_error", "images_unsupported",
			"This model does not accept images.",
		},
		{
			"upstream failure",
			&adapter.Error{Kind: adapter.ErrorUpstream, Message: "internal error at endpoint /v2"},
			http.StatusBadGateway, "server_error", "provider_error",
			"The provider could not complete this request. Try again shortly.",
		},
		{
			"provider refused",
			&adapter.Error{Kind: adapter.ErrorInvalidRequest, Message: "model gpt-x-internal is not enabled"},
			http.StatusBadGateway, "server_error", "provider_rejected",
			"The provider would not accept this request.",
		},
		{
			"server fault",
			errors.New("disk full"),
			http.StatusInternalServerError, "server_error", "internal",
			"Something went wrong on our side.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := translateUpstream(tc.err)
			got.cause = nil
			want := apiError{status: tc.status, kind: tc.kind, code: tc.code, message: tc.message}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}
