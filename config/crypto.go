package config

import (
	"crypto/rand"
	"encoding/hex"
	"io"
)

// randRead / hexEncode are tiny indirections kept in a separate file so the
// main config loader stays focused on environment parsing. They also make it
// trivial to swap entropy sources in embedded builds if ever required.

func randRead(p []byte) (int, error) { return io.ReadFull(rand.Reader, p) }

func hexEncode(p []byte) string { return hex.EncodeToString(p) }
