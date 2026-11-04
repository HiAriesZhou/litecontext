package contextmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	ContextPackMaximumEvidenceBytes = 32 * 1024
	ContextPackMaximumPayloadBytes  = 512 * 1024
)

// ValidateContextPack verifies both the portable v1 fields and the deterministic
// id/digest envelope. It deliberately has no Store, binding, credential, or
// job dependency so every client can reject an unsafe Pack before export.
func ValidateContextPack(pack GeneratedContextPack) error {
	if pack.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported Context Pack schemaVersion %q", pack.SchemaVersion)
	}
	if pack.ContractDigest != GeneratedContextModelBundleSHA256 {
		return fmt.Errorf("Context Pack contractDigest does not match the Context Model bundle")
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, pack.GeneratedAt)
	if err != nil || pack.GeneratedAt != generatedAt.UTC().Format(time.RFC3339Nano) {
		return fmt.Errorf("Context Pack generatedAt must be an RFC3339Nano UTC timestamp")
	}
	if err := validateContextPackRequest(pack.Request); err != nil {
		return err
	}
	if len(pack.Evidence) < 1 || len(pack.Evidence) > 20 {
		return fmt.Errorf("Context Pack evidence must contain between 1 and 20 entries")
	}
	for index, evidence := range pack.Evidence {
		if err := validateContextPackEvidence(evidence); err != nil {
			return fmt.Errorf("Context Pack evidence[%d]: %w", index, err)
		}
	}
	digest, err := contextPackDigest(pack)
	if err != nil {
		return err
	}
	if pack.Digest != digest {
		return fmt.Errorf("Context Pack digest does not match its canonical body")
	}
	if pack.ID != "pack_"+strings.TrimPrefix(digest, "sha256:") {
		return fmt.Errorf("Context Pack id does not match its canonical digest")
	}
	encoded, err := CanonicalJSON(pack)
	if err != nil {
		return fmt.Errorf("canonicalize Context Pack: %w", err)
	}
	if len(encoded) > ContextPackMaximumPayloadBytes {
		return fmt.Errorf("Context Pack exceeds the %d byte canonical payload limit", ContextPackMaximumPayloadBytes)
	}
	return nil
}

// FinalizeContextPack attaches the deterministic envelope to a validated body.
// Callers must supply a UTC runtime timestamp; this function never reads the
// clock, preserving reproducible tests and daemon-owned time assignment.
func FinalizeContextPack(pack GeneratedContextPack) (GeneratedContextPack, error) {
	pack.SchemaVersion = SchemaVersion
	pack.ContractDigest = GeneratedContextModelBundleSHA256
	if pack.GeneratedAt == "" {
		return GeneratedContextPack{}, fmt.Errorf("Context Pack generatedAt is required")
	}
	if err := validateContextPackBody(pack); err != nil {
		return GeneratedContextPack{}, err
	}
	digest, err := contextPackDigest(pack)
	if err != nil {
		return GeneratedContextPack{}, err
	}
	pack.Digest = digest
	pack.ID = "pack_" + strings.TrimPrefix(digest, "sha256:")
	if err := ValidateContextPack(pack); err != nil {
		return GeneratedContextPack{}, err
	}
	return pack, nil
}

func validateContextPackBody(pack GeneratedContextPack) error {
	if pack.SchemaVersion != SchemaVersion || pack.ContractDigest != GeneratedContextModelBundleSHA256 {
		return fmt.Errorf("Context Pack schemaVersion or contractDigest is invalid")
	}
	stamp, err := time.Parse(time.RFC3339Nano, pack.GeneratedAt)
	if err != nil || pack.GeneratedAt != stamp.UTC().Format(time.RFC3339Nano) {
		return fmt.Errorf("Context Pack generatedAt must be an RFC3339Nano UTC timestamp")
	}
	if err := validateContextPackRequest(pack.Request); err != nil {
		return err
	}
	if len(pack.Evidence) < 1 || len(pack.Evidence) > 20 {
		return fmt.Errorf("Context Pack evidence must contain between 1 and 20 entries")
	}
	for index, evidence := range pack.Evidence {
		if err := validateContextPackEvidence(evidence); err != nil {
			return fmt.Errorf("Context Pack evidence[%d]: %w", index, err)
		}
	}
	return nil
}

func validateContextPackRequest(request GeneratedContextPackRequest) error {
	if !portablePackText(request.Query, 512) || !portablePackText(request.ProfileID, 256) || request.Limit < 1 || request.Limit > 20 {
		return fmt.Errorf("Context Pack request is outside its contract bounds")
	}
	if request.RetrievalState != "indexed" && request.RetrievalState != "fallback" {
		return fmt.Errorf("Context Pack retrievalState is invalid")
	}
	if request.CanonicalRevision < 0 || request.ProfileRevision < 0 || request.IndexInputRevision < 0 || request.IndexGeneration < 0 {
		return fmt.Errorf("Context Pack revisions cannot be negative")
	}
	return nil
}

func validateContextPackEvidence(evidence GeneratedContextPackEvidence) error {
	if !portablePackText(evidence.ItemID, 256) || !portablePackText(evidence.ChunkID, 256) || !portablePackContent(evidence.Content) {
		return fmt.Errorf("identity or content is outside its contract bounds")
	}
	if len(evidence.Content) > ContextPackMaximumEvidenceBytes {
		return fmt.Errorf("content exceeds the %d byte evidence limit", ContextPackMaximumEvidenceBytes)
	}
	for _, heading := range evidence.HeadingPath {
		if !portablePackText(heading, 4096) {
			return fmt.Errorf("heading path is invalid")
		}
	}
	if !portablePackText(evidence.Citation.Title, 4096) || !portablePackLocator(evidence.Citation.Path) || evidence.Citation.StartLine < 1 || evidence.Citation.EndLine < evidence.Citation.StartLine {
		return fmt.Errorf("citation is invalid")
	}
	if !portablePackText(evidence.Source.ID, 256) || !portablePackText(evidence.Source.Type, 128) || !portablePackText(evidence.Source.ExternalID, 512) {
		return fmt.Errorf("source identity is invalid")
	}
	if err := portablePackURI(evidence.Source.URI); err != nil {
		return err
	}
	if !canonicalHashPattern.MatchString(evidence.ContentHash) {
		return fmt.Errorf("contentHash is invalid")
	}
	stamp, err := time.Parse(time.RFC3339Nano, evidence.ObservedAt)
	if err != nil || evidence.ObservedAt != stamp.UTC().Format(time.RFC3339Nano) {
		return fmt.Errorf("observedAt must be an RFC3339Nano UTC timestamp")
	}
	return nil
}

func portablePackText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || !norm.NFC.IsNormalString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func portablePackContent(value string) bool {
	if value == "" || len(value) > ContextPackMaximumEvidenceBytes || !utf8.ValidString(value) || !norm.NFC.IsNormalString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
			return false
		}
	}
	return true
}

func portablePackLocator(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 8192 || !utf8.ValidString(value) || !norm.NFC.IsNormalString(value) || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || path.Clean(value) != value || strings.HasPrefix(value, "../") || value == ".." {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func portablePackURI(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 8192 || !utf8.ValidString(value) || !norm.NFC.IsNormalString(value) {
		return fmt.Errorf("source URI is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.User != nil {
		return fmt.Errorf("source URI is invalid")
	}
	switch parsed.Scheme {
	case "http", "https", "urn", "litecontext":
		return nil
	default:
		return fmt.Errorf("source URI must use http, https, urn, or litecontext")
	}
}

type contextPackBody struct {
	SchemaVersion  string                         `json:"schemaVersion"`
	GeneratedAt    string                         `json:"generatedAt"`
	ContractDigest string                         `json:"contractDigest"`
	Request        GeneratedContextPackRequest    `json:"request"`
	Evidence       []GeneratedContextPackEvidence `json:"evidence"`
}

func contextPackDigest(pack GeneratedContextPack) (string, error) {
	body := contextPackBody{SchemaVersion: pack.SchemaVersion, GeneratedAt: pack.GeneratedAt, ContractDigest: pack.ContractDigest, Request: pack.Request, Evidence: pack.Evidence}
	encoded, err := CanonicalJSON(body)
	if err != nil {
		return "", fmt.Errorf("canonicalize Context Pack body: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
