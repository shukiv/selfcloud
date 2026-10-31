// SPDX-License-Identifier: AGPL-3.0-or-later

package wopi

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"time"
)

// Headers Collabora sends with every WOPI request.
const (
	HeaderProof     = "X-WOPI-Proof"
	HeaderProofOld  = "X-WOPI-ProofOld"
	HeaderTimeStamp = "X-WOPI-TimeStamp"
)

// MaxProofAge is how old a request's timestamp may be (the WOPI proof-key rule: no more than 20 minutes).
// MaxProofAhead is how far in the future one may be; the spec does not say, and Selfcloud allows only a small
// clock skew, so a timestamp far ahead is refused.
const (
	MaxProofAge   = 20 * time.Minute
	MaxProofAhead = 5 * time.Minute
)

// TicksAtUnixEpoch is the .NET tick count (100 ns units since 0001-01-01 UTC) at 1970-01-01 UTC. The WOPI proof page
// says X-WOPI-TimeStamp is converted to a long and does not give its unit or epoch. Selfcloud reads it as .NET ticks,
// and the eight published proof-key vectors (proof_vectors_test.go) verify under that reading. The vectors are
// Microsoft's, not Collabora's: how a live Collabora signs is still unconfirmed (decision 31). If the unit were wrong,
// every proof would fail the window check, so the failure is closed, not open.
const TicksAtUnixEpoch int64 = 621355968000000000

// TimestampFresh reports whether a WOPI timestamp lies within the proof window at now.
func TimestampFresh(ts int64, now time.Time) bool {
	sent := time.Unix(0, (ts-TicksAtUnixEpoch)*100)
	age := now.Sub(sent)
	return age <= MaxProofAge && age >= -MaxProofAhead
}

// ExpectedProof builds the byte string Collabora signs: for each of the access token, the request URL in uppercase,
// and the timestamp, a 4-byte length (big-endian) and then the bytes. The timestamp is the 8-byte big-endian value of
// the header read as a 64-bit integer. The URL must be the one Collabora used, query string included.
//
// Byte order: the WOPI page calls an EncodeNumber helper and does not define it. Big-endian is what Microsoft's
// published proof-key vectors use (proof_vectors_test.go), but those are not Collabora's output, so a live Collabora
// is still unconfirmed (decision 31).
func ExpectedProof(accessToken, url string, ts int64) []byte {
	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(ts))
	parts := [][]byte{[]byte(accessToken), []byte(strings.ToUpper(url)), tsBytes}
	var out []byte
	for _, p := range parts {
		out = binary.BigEndian.AppendUint32(out, uint32(len(p)))
		out = append(out, p...)
	}
	return out
}

// VerifyProof reports whether one of the three signature combinations the WOPI page lists is valid: the X-WOPI-Proof
// header against the current key, X-WOPI-ProofOld against the current key, and X-WOPI-Proof against the old key. A
// missing or malformed header, or a missing key, makes its combination false.
func (doc *Document) VerifyProof(accessToken, url string, ts int64, proof, proofOld string) bool {
	if doc == nil || doc.Current == nil {
		return false
	}
	expected := ExpectedProof(accessToken, url, ts)
	digest := sha256.Sum256(expected)
	if verifySig(doc.Current, digest[:], proof) || verifySig(doc.Current, digest[:], proofOld) {
		return true
	}
	return doc.Old != nil && verifySig(doc.Old, digest[:], proof)
}

// verifySig reports whether sig, base64 of an RSA PKCS#1 v1.5 signature with SHA-256, is valid for digest under key.
func verifySig(key *rsa.PublicKey, digest []byte, sig string) bool {
	if sig == "" {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, digest, raw) == nil
}
