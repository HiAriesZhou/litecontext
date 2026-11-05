package contextmodel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// CanonicalSourceRecord is the F Context Model portable source record. It is
// intentionally distinct from the pre-F SourceRecord facade while callers are
// clientized. Source health, bindings, credentials, jobs, and cursors remain
// device-local and must not be added here.
type CanonicalSourceRecord struct {
	SchemaVersion     string         `json:"schemaVersion"`
	ID                string         `json:"id"`
	Type              string         `json:"type"`
	Name              string         `json:"name"`
	Extensions        map[string]any `json:"extensions,omitempty"`
	CreatedAt         string         `json:"createdAt"`
	UpdatedAt         string         `json:"updatedAt"`
	RecordRevision    int64          `json:"recordRevision"`
	CanonicalRevision int64          `json:"canonicalRevision"`
}

// SourceHealth is a device-local operational projection. It may be returned
// only by trusted local APIs and is never serialized as portable Context Model
// data or Context Pack evidence.
type SourceHealth struct {
	SourceID      string `json:"sourceId"`
	Status        string `json:"status"`
	LastError     string `json:"lastError,omitempty"`
	UpdatedAt     string `json:"updatedAt"`
	BindingStatus string `json:"bindingStatus,omitempty"`
}

// CanonicalContextItem is the F portable content record. Its ID is derived
// from the canonical source ID plus the producer-owned external ID.
type CanonicalContextItem struct {
	SchemaVersion     string              `json:"schemaVersion"`
	ID                string              `json:"id"`
	Source            CanonicalSourceRef  `json:"source"`
	Content           Content             `json:"content"`
	Provenance        CanonicalProvenance `json:"provenance"`
	Attributes        CanonicalAttributes `json:"attributes,omitempty"`
	Extensions        map[string]any      `json:"extensions,omitempty"`
	RecordRevision    int64               `json:"recordRevision"`
	CanonicalRevision int64               `json:"canonicalRevision"`
}

type CanonicalSourceRef struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	ExternalID string `json:"externalId"`
	Connector  string `json:"connector,omitempty"`
}

type CanonicalProvenance struct {
	SourceURI   string `json:"sourceURI,omitempty"`
	Locator     string `json:"locator,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty" nonempty:"true"`
	UpdatedAt   string `json:"updatedAt,omitempty" nonempty:"true"`
	ObservedAt  string `json:"observedAt"`
	ContentHash string `json:"contentHash"`
	RecordHash  string `json:"recordHash"`
}

type CanonicalAttributes struct {
	Tags       []string `json:"tags,omitempty"`
	Visibility string   `json:"visibility,omitempty" enum:"private,team,public"`
	Confidence string   `json:"confidence,omitempty" enum:"low,medium,high"`
	Stale      bool     `json:"stale,omitempty"`
	WebURL     string   `json:"webURL,omitempty"`
}

// CanonicalContextProfile is the portable retrieval boundary. Device-local
// health, binding, jobs, credentials, and index state belong to neither this
// record nor its JSON schema.
type CanonicalContextProfile struct {
	SchemaVersion     string          `json:"schemaVersion"`
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Description       string          `json:"description,omitempty"`
	Sources           []ProfileSource `json:"sources"`
	IncludeTags       []string        `json:"includeTags,omitempty"`
	ExcludeTags       []string        `json:"excludeTags,omitempty"`
	IncludeGlobs      []string        `json:"includeGlobs,omitempty"`
	ExcludeGlobs      []string        `json:"excludeGlobs,omitempty"`
	Visibility        []string        `json:"visibility,omitempty"`
	Privacy           map[string]any  `json:"privacy,omitempty"`
	Freshness         map[string]any  `json:"freshness,omitempty"`
	Retrieval         map[string]any  `json:"retrieval,omitempty"`
	CreatedAt         string          `json:"createdAt"`
	UpdatedAt         string          `json:"updatedAt"`
	RecordRevision    int64           `json:"recordRevision"`
	CanonicalRevision int64           `json:"canonicalRevision"`
}

var (
	canonicalSourceIDPattern = regexp.MustCompile(`^src_[0-9a-f]{32}$`)
	canonicalItemIDPattern   = regexp.MustCompile(`^itm_[0-9a-f]{64}$`)
	canonicalHashPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	extensionKeyPattern      = regexp.MustCompile(`^(litecontext|connector\.[a-z0-9][a-z0-9-]{0,62})\.[a-z][a-z0-9_.-]{0,127}$`)
)

const maximumPortableInteger = int64(1<<53 - 1)

func (record CanonicalSourceRecord) Validate() error {
	if record.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported canonical SourceRecord schemaVersion %q", record.SchemaVersion)
	}
	if !canonicalSourceIDPattern.MatchString(record.ID) {
		return fmt.Errorf("canonical SourceRecord id must be src_ plus 128 lowercase random bits")
	}
	if !canonicalText(record.Type, 128) || !canonicalText(record.Name, 512) {
		return fmt.Errorf("canonical SourceRecord type or name is invalid")
	}
	if err := validateRequiredTimestamp("canonical SourceRecord createdAt", record.CreatedAt); err != nil {
		return err
	}
	if err := validateRequiredTimestamp("canonical SourceRecord updatedAt", record.UpdatedAt); err != nil {
		return err
	}
	if record.RecordRevision < 0 || record.CanonicalRevision < 0 {
		return fmt.Errorf("canonical SourceRecord revisions cannot be negative")
	}
	return validateExtensions(record.Extensions)
}

func (health SourceHealth) Validate() error {
	if !canonicalSourceIDPattern.MatchString(health.SourceID) {
		return fmt.Errorf("SourceHealth sourceId is invalid")
	}
	switch health.Status {
	case "active", "authorization_required", "error", "disabled":
	default:
		return fmt.Errorf("unsupported SourceHealth status %q", health.Status)
	}
	if len(health.LastError) > 4096 {
		return fmt.Errorf("SourceHealth lastError exceeds 4096 characters")
	}
	return validateRequiredTimestamp("SourceHealth updatedAt", health.UpdatedAt)
}

func (item CanonicalContextItem) Validate() error {
	if item.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported canonical ContextItem schemaVersion %q", item.SchemaVersion)
	}
	if !canonicalItemIDPattern.MatchString(item.ID) || item.ID != CanonicalItemID(item.Source.ID, item.Source.ExternalID) {
		return fmt.Errorf("canonical ContextItem id does not match source and external identity")
	}
	if !canonicalSourceIDPattern.MatchString(item.Source.ID) || !canonicalText(item.Source.Type, 128) ||
		!canonicalText(item.Source.ExternalID, 512) || (item.Source.Connector != "" && !canonicalText(item.Source.Connector, 128)) {
		return fmt.Errorf("canonical ContextItem source identity is invalid")
	}
	if err := validateExternalIdentity(item.Source.ExternalID); err != nil {
		return err
	}
	if !validContent(item.Content) {
		return fmt.Errorf("canonical ContextItem content is invalid")
	}
	if err := item.Provenance.validate(); err != nil {
		return err
	}
	if item.Provenance.ContentHash != item.ComputedContentHash() {
		return fmt.Errorf("canonical ContextItem content hash does not match content")
	}
	if item.Provenance.RecordHash != item.ComputedRecordHash() {
		return fmt.Errorf("canonical ContextItem record hash does not match portable record")
	}
	if err := item.Attributes.validate(); err != nil {
		return err
	}
	if item.RecordRevision < 0 || item.CanonicalRevision < 0 {
		return fmt.Errorf("canonical ContextItem revisions cannot be negative")
	}
	return validateExtensions(item.Extensions)
}

func (profile CanonicalContextProfile) Validate() error {
	legacy := ContextProfile{
		SchemaVersion: profile.SchemaVersion, ID: profile.ID, Name: profile.Name, Description: profile.Description,
		Sources: profile.Sources, IncludeTags: profile.IncludeTags, ExcludeTags: profile.ExcludeTags,
		IncludeGlobs: profile.IncludeGlobs, ExcludeGlobs: profile.ExcludeGlobs, Visibility: profile.Visibility,
		Privacy: profile.Privacy, Freshness: profile.Freshness, Retrieval: profile.Retrieval,
		CreatedAt: profile.CreatedAt, UpdatedAt: profile.UpdatedAt,
	}
	if err := legacy.Validate(); err != nil {
		return fmt.Errorf("canonical ContextProfile: %w", err)
	}
	for _, source := range profile.Sources {
		if !canonicalSourceIDPattern.MatchString(source.SourceID) {
			return fmt.Errorf("canonical ContextProfile sourceId is invalid")
		}
	}
	if profile.RecordRevision < 0 || profile.CanonicalRevision < 0 {
		return fmt.Errorf("canonical ContextProfile revisions cannot be negative")
	}
	return nil
}

func (provenance CanonicalProvenance) validate() error {
	if err := validateOptionalTimestamp("canonical ContextItem createdAt", provenance.CreatedAt); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("canonical ContextItem updatedAt", provenance.UpdatedAt); err != nil {
		return err
	}
	if err := validateRequiredTimestamp("canonical ContextItem observedAt", provenance.ObservedAt); err != nil {
		return err
	}
	if !canonicalHashPattern.MatchString(provenance.ContentHash) || !canonicalHashPattern.MatchString(provenance.RecordHash) {
		return fmt.Errorf("canonical ContextItem hashes are invalid")
	}
	if err := validatePortableURI(provenance.SourceURI); err != nil {
		return err
	}
	return validateRelativeLocator(provenance.Locator)
}

func (attributes CanonicalAttributes) validate() error {
	if len(attributes.Tags) > 256 || (attributes.Visibility != "" && attributes.Visibility != "private" && attributes.Visibility != "team" && attributes.Visibility != "public") ||
		(attributes.Confidence != "" && attributes.Confidence != "low" && attributes.Confidence != "medium" && attributes.Confidence != "high") {
		return fmt.Errorf("canonical ContextItem attributes are invalid")
	}
	for _, tag := range attributes.Tags {
		if !canonicalText(tag, 512) {
			return fmt.Errorf("canonical ContextItem tag is invalid")
		}
	}
	if attributes.WebURL != "" {
		return validatePortableURI(attributes.WebURL)
	}
	return nil
}

func CanonicalItemID(sourceID, externalID string) string {
	digest := sha256.Sum256([]byte(norm.NFC.String(sourceID) + "\x00" + norm.NFC.String(externalID)))
	return "itm_" + hex.EncodeToString(digest[:])
}

func canonicalHash(value any) string {
	digest, _ := CanonicalDigest(value)
	return digest
}

// CanonicalJSON encodes a Context Model value using RFC 8785 JCS. Interchange
// artifacts such as Context Pack must use these exact bytes rather than the
// ordinary Go JSON encoder so their digest is stable across runtime clients.
func CanonicalJSON(value any) ([]byte, error) { return canonicalJSON(value) }

// CanonicalDigest returns the sha256: digest of CanonicalJSON(value).
func CanonicalDigest(value any) (string, error) {
	encoded, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func canonicalJSON(value any) ([]byte, error) {
	buffer := bytes.Buffer{}
	if err := appendCanonicalJSON(&buffer, reflect.ValueOf(value)); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// appendCanonicalJSON implements RFC 8785 JSON Canonicalization Scheme (JCS)
// for the JSON values admitted by Context Model v1. Floats are rejected by
// portable extension validation, but their encoding still follows ECMAScript
// number formatting so this serializer cannot silently drift if it is reused.
func appendCanonicalJSON(buffer *bytes.Buffer, value reflect.Value) error {
	if !value.IsValid() {
		buffer.WriteString("null")
		return nil
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			buffer.WriteString("null")
			return nil
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Bool:
		buffer.WriteString(strconv.FormatBool(value.Bool()))
	case reflect.String:
		return appendJCSString(buffer, value.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		buffer.WriteString(strconv.FormatInt(value.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		buffer.WriteString(strconv.FormatUint(value.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		if math.IsNaN(value.Float()) || math.IsInf(value.Float(), 0) {
			return fmt.Errorf("non-finite numbers are not canonical JSON")
		}
		buffer.WriteString(jcsNumber(value.Float(), value.Type().Bits()))
	case reflect.Slice, reflect.Array:
		buffer.WriteByte('[')
		for index := 0; index < value.Len(); index++ {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := appendCanonicalJSON(buffer, value.Index(index)); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("canonical JSON object key is not a string")
		}
		keys := value.MapKeys()
		for _, key := range keys {
			if !utf8.ValidString(key.String()) {
				return fmt.Errorf("canonical JSON object key is not valid UTF-8")
			}
		}
		sort.Slice(keys, func(left, right int) bool { return jcsStringLess(keys[left].String(), keys[right].String()) })
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := appendJCSString(buffer, key.String()); err != nil {
				return err
			}
			buffer.WriteByte(':')
			if err := appendCanonicalJSON(buffer, value.MapIndex(key)); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
	case reflect.Struct:
		type field struct {
			name  string
			value reflect.Value
		}
		fields := []field{}
		typeOf := value.Type()
		for index := 0; index < value.NumField(); index++ {
			metadata := typeOf.Field(index)
			if metadata.PkgPath != "" {
				continue
			}
			tag := strings.Split(metadata.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			name := tag[0]
			if name == "" {
				name = metadata.Name
			}
			fieldValue := value.Field(index)
			if len(tag) > 1 && tag[1] == "omitempty" && isJSONEmptyValue(fieldValue) {
				continue
			}
			fields = append(fields, field{name: name, value: fieldValue})
		}
		sort.Slice(fields, func(left, right int) bool { return jcsStringLess(fields[left].name, fields[right].name) })
		buffer.WriteByte('{')
		for index, field := range fields {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := appendJCSString(buffer, field.name); err != nil {
				return err
			}
			buffer.WriteByte(':')
			if err := appendCanonicalJSON(buffer, field.value); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON value %s", value.Kind())
	}
	return nil
}

// isJSONEmptyValue mirrors encoding/json's omitempty behavior. Canonical JSON
// must describe the same wire object that a client decodes; in particular a
// non-nil empty slice is omitted rather than being hashed as a distinct []
// value that disappears after a JSON round trip.
func isJSONEmptyValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool:
		return !value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return value.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return value.IsNil()
	default:
		return false
	}
}

func appendJCSString(buffer *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("canonical JSON string is not valid UTF-8")
	}
	buffer.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"':
			buffer.WriteString(`\"`)
		case '\\':
			buffer.WriteString(`\\`)
		case '\b':
			buffer.WriteString(`\b`)
		case '\t':
			buffer.WriteString(`\t`)
		case '\n':
			buffer.WriteString(`\n`)
		case '\f':
			buffer.WriteString(`\f`)
		case '\r':
			buffer.WriteString(`\r`)
		default:
			if character < 0x20 {
				buffer.WriteString(fmt.Sprintf(`\u%04x`, character))
			} else {
				buffer.WriteRune(character)
			}
		}
	}
	buffer.WriteByte('"')
	return nil
}

func jcsStringLess(left, right string) bool {
	leftUnits, rightUnits := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	for index := 0; index < len(leftUnits) && index < len(rightUnits); index++ {
		if leftUnits[index] != rightUnits[index] {
			return leftUnits[index] < rightUnits[index]
		}
	}
	return len(leftUnits) < len(rightUnits)
}

func jcsNumber(value float64, bitSize int) string {
	if value == 0 {
		return "0"
	}
	abs := math.Abs(value)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(value, 'f', -1, bitSize)
	}
	encoded := strconv.FormatFloat(value, 'g', -1, bitSize)
	if exponent := strings.IndexByte(encoded, 'e'); exponent >= 0 {
		prefix, suffix := encoded[:exponent+1], encoded[exponent+1:]
		sign := ""
		if strings.HasPrefix(suffix, "+") || strings.HasPrefix(suffix, "-") {
			sign, suffix = suffix[:1], suffix[1:]
		}
		suffix = strings.TrimLeft(suffix, "0")
		if suffix == "" {
			suffix = "0"
		}
		return prefix + sign + suffix
	}
	return encoded
}

func validContent(content Content) bool {
	if content.Format != "text" && content.Format != "markdown" && content.Format != "html" {
		return false
	}
	return canonicalText(content.Title, 4096) && len(content.Body) <= 8*1024*1024 && len(content.Summary) <= 16*1024 &&
		norm.NFC.IsNormalString(content.Body) && norm.NFC.IsNormalString(content.Summary)
}

func validatePortableURI(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 8192 || !norm.NFC.IsNormalString(value) {
		return fmt.Errorf("portable URI is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Scheme == "" || (parsed.Scheme != "https" && parsed.Scheme != "http" && parsed.Scheme != "urn" && parsed.Scheme != "litecontext") {
		return fmt.Errorf("portable URI must use http, https, urn, or litecontext without credentials")
	}
	return nil
}

func drivePath(value string) bool {
	return len(value) >= 2 && value[1] == ':' && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z'))
}
func validateExternalIdentity(value string) error {
	if strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || drivePath(value) || strings.HasPrefix(value, "~/") || strings.HasPrefix(strings.ToLower(value), "file:") {
		return fmt.Errorf("external identity must not contain a device path")
	}
	if parsed, err := url.Parse(value); (err == nil && parsed.User != nil) || (strings.Contains(value, "://") && err != nil) {
		return fmt.Errorf("external identity must not contain URL credentials")
	}

	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return fmt.Errorf("external identity must not contain parent traversal")
		}
	}
	return nil
}

func validateRelativeLocator(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 8192 || !norm.NFC.IsNormalString(value) || strings.HasPrefix(value, "/") || drivePath(value) || strings.HasPrefix(value, "~/") || strings.HasPrefix(strings.ToLower(value), "file:") || strings.Contains(value, "\\") || path.Clean(value) != value || strings.HasPrefix(value, "../") || value == ".." {
		return fmt.Errorf("portable locator must be an NFC relative slash path")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("portable locator contains a control character")
		}
	}
	return nil
}

func validateExtensions(extensions map[string]any) error {
	if len(extensions) > 32 {
		return fmt.Errorf("portable extensions contain too many entries")
	}
	for key, value := range extensions {
		if !extensionKeyPattern.MatchString(key) || !norm.NFC.IsNormalString(key) {
			return fmt.Errorf("portable extension key %q is not approved", key)
		}
		if err := validateExtensionValue(value, 0); err != nil {
			return fmt.Errorf("portable extension %q: %w", key, err)
		}
	}
	encoded, err := canonicalJSON(extensions)
	if err != nil || len(encoded) > 64*1024 {
		return fmt.Errorf("portable extensions exceed their canonical bounds")
	}
	return nil
}

func validateExtensionValue(value any, depth int) error {
	if depth > 4 {
		return fmt.Errorf("nested too deeply")
	}
	switch typed := value.(type) {
	case nil, bool, string:
		if text, ok := typed.(string); ok && (!norm.NFC.IsNormalString(text) || len(text) > 4096) {
			return fmt.Errorf("string is invalid")
		}
		return nil
	case int, int8, int16, int32, int64:
		integer := reflect.ValueOf(typed).Int()
		if integer < -maximumPortableInteger || integer > maximumPortableInteger {
			return fmt.Errorf("integer exceeds JavaScript safe range")
		}
		return nil
	case uint, uint8, uint16, uint32, uint64:
		if reflect.ValueOf(typed).Uint() > uint64(maximumPortableInteger) {
			return fmt.Errorf("integer exceeds JavaScript safe range")
		}
		return nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < -float64(maximumPortableInteger) || typed > float64(maximumPortableInteger) {
			return fmt.Errorf("number is not a JavaScript-safe integer")
		}
		return nil
	case []any:
		if len(typed) > 128 {
			return fmt.Errorf("array contains too many values")
		}
		for _, entry := range typed {
			if err := validateExtensionValue(entry, depth+1); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		if len(typed) > 32 {
			return fmt.Errorf("object contains too many values")
		}
		for key, entry := range typed {
			if !canonicalText(key, 128) {
				return fmt.Errorf("object key is invalid")
			}
			if err := validateExtensionValue(entry, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("value type %T is not portable JSON", value)
	}
}

func canonicalText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || !norm.NFC.IsNormalString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validateRequiredTimestamp(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return validateOptionalTimestamp(name, value)
}

func validateOptionalTimestamp(name, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || !strings.HasSuffix(value, "Z") {
		return fmt.Errorf("%s must be RFC3339Nano UTC", name)
	}
	return nil
}
