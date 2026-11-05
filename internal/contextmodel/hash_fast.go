package contextmodel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/text/unicode/norm"
)

func (item CanonicalContextItem) ComputedContentHash() string {
	format := norm.NFC.String(item.Content.Format)
	title := norm.NFC.String(item.Content.Title)
	body := norm.NFC.String(item.Content.Body)
	summary := norm.NFC.String(item.Content.Summary)
	var buf bytes.Buffer
	buf.Grow(len(body) + len(title) + len(summary) + 64)
	if err := appendContentHashJSON(&buf, format, title, body, summary); err != nil {
		return ""
	}
	return digestBytes(buf.Bytes())
}

func (item CanonicalContextItem) ComputedRecordHash() string {
	if len(item.Extensions) > 0 {
		return canonicalHash(recordHashPayload(item))
	}
	var buf bytes.Buffer
	buf.Grow(len(item.Content.Body) + 256)
	if err := appendRecordHashJSON(&buf, item); err != nil {
		return ""
	}
	return digestBytes(buf.Bytes())
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func appendContentHashJSON(buf *bytes.Buffer, format, title, body, summary string) error {
	buf.WriteString(`{"body":`)
	if err := appendJCSString(buf, body); err != nil {
		return err
	}
	buf.WriteString(`,"format":`)
	if err := appendJCSString(buf, format); err != nil {
		return err
	}
	if summary != "" {
		buf.WriteString(`,"summary":`)
		if err := appendJCSString(buf, summary); err != nil {
			return err
		}
	}
	buf.WriteString(`,"title":`)
	if err := appendJCSString(buf, title); err != nil {
		return err
	}
	buf.WriteByte('}')
	return nil
}

func appendRecordHashJSON(buf *bytes.Buffer, item CanonicalContextItem) error {
	buf.WriteString(`{"attributes":`)
	if err := appendAttributesJSON(buf, item.Attributes); err != nil {
		return err
	}
	buf.WriteString(`,"content":`)
	if err := appendContentHashJSON(buf, item.Content.Format, item.Content.Title, item.Content.Body, item.Content.Summary); err != nil {
		return err
	}
	buf.WriteString(`,"id":`)
	if err := appendJCSString(buf, item.ID); err != nil {
		return err
	}
	buf.WriteString(`,"provenance":`)
	if err := appendRecordProvenanceJSON(buf, item.Provenance); err != nil {
		return err
	}
	buf.WriteString(`,"schemaVersion":`)
	if err := appendJCSString(buf, item.SchemaVersion); err != nil {
		return err
	}
	buf.WriteString(`,"source":`)
	if err := appendSourceRefJSON(buf, item.Source); err != nil {
		return err
	}
	buf.WriteByte('}')
	return nil
}

func appendRecordProvenanceJSON(buf *bytes.Buffer, provenance CanonicalProvenance) error {
	buf.WriteString(`{"contentHash":`)
	if err := appendJCSString(buf, provenance.ContentHash); err != nil {
		return err
	}
	if provenance.CreatedAt != "" {
		buf.WriteString(`,"createdAt":`)
		if err := appendJCSString(buf, provenance.CreatedAt); err != nil {
			return err
		}
	}
	if provenance.Locator != "" {
		buf.WriteString(`,"locator":`)
		if err := appendJCSString(buf, provenance.Locator); err != nil {
			return err
		}
	}
	if provenance.SourceURI != "" {
		buf.WriteString(`,"sourceURI":`)
		if err := appendJCSString(buf, provenance.SourceURI); err != nil {
			return err
		}
	}
	if provenance.UpdatedAt != "" {
		buf.WriteString(`,"updatedAt":`)
		if err := appendJCSString(buf, provenance.UpdatedAt); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

func appendSourceRefJSON(buf *bytes.Buffer, source CanonicalSourceRef) error {
	buf.WriteByte('{')
	first := true
	write := func(key, value string, optional bool) error {
		if optional && value == "" {
			return nil
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		buf.WriteByte('"')
		buf.WriteString(key)
		buf.WriteString(`":`)
		return appendJCSString(buf, value)
	}
	if err := write("connector", source.Connector, true); err != nil {
		return err
	}
	if err := write("externalId", source.ExternalID, false); err != nil {
		return err
	}
	if err := write("id", source.ID, false); err != nil {
		return err
	}
	if err := write("type", source.Type, false); err != nil {
		return err
	}
	buf.WriteByte('}')
	return nil
}

func appendAttributesJSON(buf *bytes.Buffer, attributes CanonicalAttributes) error {
	buf.WriteByte('{')
	first := true
	comma := func() {
		if !first {
			buf.WriteByte(',')
		}
		first = false
	}
	if attributes.Confidence != "" {
		comma()
		buf.WriteString(`"confidence":`)
		if err := appendJCSString(buf, attributes.Confidence); err != nil {
			return err
		}
	}
	if attributes.Stale {
		comma()
		buf.WriteString(`"stale":true`)
	}
	if len(attributes.Tags) > 0 {
		comma()
		buf.WriteString(`"tags":[`)
		for index, tag := range attributes.Tags {
			if index > 0 {
				buf.WriteByte(',')
			}
			if err := appendJCSString(buf, tag); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	}
	if attributes.Visibility != "" {
		comma()
		buf.WriteString(`"visibility":`)
		if err := appendJCSString(buf, attributes.Visibility); err != nil {
			return err
		}
	}
	if attributes.WebURL != "" {
		comma()
		buf.WriteString(`"webURL":`)
		if err := appendJCSString(buf, attributes.WebURL); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

func recordHashPayload(item CanonicalContextItem) any {
	return struct {
		SchemaVersion string             `json:"schemaVersion"`
		ID            string             `json:"id"`
		Source        CanonicalSourceRef `json:"source"`
		Content       Content            `json:"content"`
		Provenance    struct {
			SourceURI   string `json:"sourceURI,omitempty"`
			Locator     string `json:"locator,omitempty"`
			CreatedAt   string `json:"createdAt,omitempty" nonempty:"true"`
			UpdatedAt   string `json:"updatedAt,omitempty" nonempty:"true"`
			ContentHash string `json:"contentHash"`
		} `json:"provenance"`
		Attributes CanonicalAttributes `json:"attributes,omitempty"`
		Extensions map[string]any      `json:"extensions,omitempty"`
	}{
		SchemaVersion: item.SchemaVersion,
		ID:            item.ID,
		Source:        item.Source,
		Content:       item.Content,
		Provenance: struct {
			SourceURI   string `json:"sourceURI,omitempty"`
			Locator     string `json:"locator,omitempty"`
			CreatedAt   string `json:"createdAt,omitempty" nonempty:"true"`
			UpdatedAt   string `json:"updatedAt,omitempty" nonempty:"true"`
			ContentHash string `json:"contentHash"`
		}{item.Provenance.SourceURI, item.Provenance.Locator, item.Provenance.CreatedAt, item.Provenance.UpdatedAt, item.Provenance.ContentHash},
		Attributes: item.Attributes,
		Extensions: item.Extensions,
	}
}
