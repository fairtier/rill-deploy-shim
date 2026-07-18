module github.com/fairtier/rill-deploy-shim

// Zero external dependencies on purpose — the whole proxy is net/http +
// httputil + encoding/json, so the image builds without a go.sum and stays
// tiny. Keep it that way; if you reach for a dep, reconsider.
go 1.26
