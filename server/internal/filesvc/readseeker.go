package filesvc

import "bytes"

// newReadSeeker adapts a byte slice to io.ReadSeeker for http.ServeContent,
// which needs seeking to support range requests.
func newReadSeeker(b []byte) *bytes.Reader {
	return bytes.NewReader(b)
}
