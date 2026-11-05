package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/HiAriesZhou/litecontext/internal/strictjson"
)

const maxEmissionBytes = 64 << 10

// Emission is the delivery record for one host injection.
// Emitted is the number of entries returned. Text is the rendered context.
type Emission struct {
	EventID      string          `json:"eventId"`
	Text         string          `json:"text"`
	Selected     int             `json:"selected"`
	Emitted      int             `json:"emitted"`
	Acknowledged string          `json:"acknowledged" enum:"unknown"`
	Used         string          `json:"used" enum:"unknown"`
	ElapsedMs    *int            `json:"elapsedMs,omitempty"`
	Omissions    *[]Omission     `json:"omissions,omitempty"`
	Entries      *[]EmittedEntry `json:"entries,omitempty"`
}

// SaveEmission records the injection returned to the host for one event.
// A later delivery with the same event ID replaces the previous record.
// The file is events/delivery/<eventID>.json, separate from the event record.
func SaveEmission(ctx context.Context, bundleDir, eventID string, input DeliveryInput) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !validID(eventID) || input.Text == "" || input.Selected < 0 || input.Emitted < 0 || input.Emitted > input.Selected {
		return fmt.Errorf("invalid delivery")
	}
	if (input.Acknowledged != "" && input.Acknowledged != "unknown") || (input.Used != "" && input.Used != "unknown") {
		return fmt.Errorf("invalid delivery")
	}
	if input.ElapsedMs != nil && (*input.ElapsedMs < 0 || *input.ElapsedMs > 600000) {
		return fmt.Errorf("invalid delivery")
	}
	omissions, err := storedOmissions(input.Omissions)
	if err != nil {
		return err
	}
	entries, err := storedEntries(input.Entries)
	if err != nil {
		return err
	}
	if err := plainText(input.Text, maxEmissionBytes); err != nil {
		return fmt.Errorf("invalid delivery")
	}
	_, payload, err := fitEmission(Emission{
		EventID: eventID, Text: input.Text, Selected: input.Selected, Emitted: input.Emitted,
		Acknowledged: "unknown", Used: "unknown", ElapsedMs: input.ElapsedMs, Omissions: omissions, Entries: entries,
	})
	if err != nil {
		return err
	}
	events, release, err := openEventDir(ctx, bundleDir)
	if err != nil {
		return fmt.Errorf("delivery storage failed")
	}
	defer release()
	info, err := events.Lstat("delivery")
	if errors.Is(err, os.ErrNotExist) {
		if err = events.Mkdir("delivery", 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("delivery storage failed")
		}
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("delivery storage failed")
	}
	delivery, err := events.OpenRoot("delivery")
	if err != nil {
		return fmt.Errorf("delivery storage failed")
	}
	defer delivery.Close()
	return replaceFile(delivery, eventID+".json", append(payload, '\n'))
}

const maxStoredOmissions = 100

func storedOmissions(input *[]Omission) (*[]Omission, error) {
	if input == nil {
		return nil, nil
	}
	for _, item := range *input {
		if err := validOmission(item); err != nil {
			return nil, err
		}
	}
	list := make([]Omission, 0, len(*input))
	list = append(list, (*input)...)
	if len(list) > maxStoredOmissions {
		list = append(list[:maxStoredOmissions], Omission{Role: "omission", Reason: "truncated"})
	}
	return &list, nil
}

func validOmission(item Omission) error {
	switch item.Role {
	case "task", "memory", "pin", "knowledge", "omission":
	default:
		return fmt.Errorf("invalid delivery")
	}
	switch item.Reason {
	case "budget", "unavailable", "truncated":
	default:
		return fmt.Errorf("invalid delivery")
	}
	if item.Reason == "truncated" && item.Role != "omission" {
		return fmt.Errorf("invalid delivery")
	}
	if len(item.Locator) > 8192 || strings.Contains(item.Locator, "\\") || strings.Contains(item.Locator, "\n") {
		return fmt.Errorf("invalid delivery")
	}
	for _, character := range item.Locator {
		if character < 0x20 {
			return fmt.Errorf("invalid delivery")
		}
	}
	if item.Ref == nil {
		return nil
	}
	if item.Ref.SourceID == "" || item.Ref.ItemID == "" || len(item.Ref.SourceID) > 128 || len(item.Ref.ItemID) > 256 {
		return fmt.Errorf("invalid delivery")
	}
	return nil
}

const maxStoredEntries = 100
const maxEmissionFileBytes = maxEmissionBytes + 4096

func storedEntries(input *[]EmittedEntry) (*[]EmittedEntry, error) {
	if input == nil {
		return nil, nil
	}
	for _, item := range *input {
		if err := validEmittedEntry(item); err != nil {
			return nil, err
		}
	}
	list := make([]EmittedEntry, 0, len(*input))
	list = append(list, (*input)...)
	if len(list) > maxStoredEntries {
		list = append(list[:maxStoredEntries], EmittedEntry{Role: "omission", Reason: "truncated"})
	}
	return &list, nil
}

func validEmittedEntry(item EmittedEntry) error {
	if item.Reason == "truncated" {
		if item.Role != "omission" || item.Locator != "" || item.Ref != nil {
			return fmt.Errorf("invalid delivery")
		}
		return nil
	}
	if item.Reason != "" {
		return fmt.Errorf("invalid delivery")
	}
	switch item.Role {
	case "task", "memory", "pin", "knowledge":
	default:
		return fmt.Errorf("invalid delivery")
	}
	if err := validDeliveryLocator(item.Locator); err != nil {
		return err
	}
	return validDeliveryRef(item.Ref)
}

func validDeliveryLocator(locator string) error {
	if len(locator) > 8192 || strings.Contains(locator, "\\") || strings.Contains(locator, "\n") {
		return fmt.Errorf("invalid delivery")
	}
	for _, character := range locator {
		if character < 0x20 {
			return fmt.Errorf("invalid delivery")
		}
	}
	return nil
}

func validDeliveryRef(ref *Ref) error {
	if ref == nil {
		return nil
	}
	if ref.SourceID == "" || ref.ItemID == "" || len(ref.SourceID) > 128 || len(ref.ItemID) > 256 || len(ref.RecordHash) > 256 {
		return fmt.Errorf("invalid delivery")
	}
	if strings.Contains(ref.RecordHash, "\\") || strings.Contains(ref.RecordHash, "\n") || strings.Contains(ref.SourceID, "\n") || strings.Contains(ref.ItemID, "\n") {
		return fmt.Errorf("invalid delivery")
	}
	for _, character := range ref.RecordHash + ref.SourceID + ref.ItemID {
		if character < 0x20 {
			return fmt.Errorf("invalid delivery")
		}
	}
	if ref.StartLine < 0 || ref.EndLine < 0 || ref.StartLine > 10000000 || ref.EndLine > 10000000 {
		return fmt.Errorf("invalid delivery")
	}
	return nil
}

// fitEmission keeps the delivery file inside the read limit.
// Entry identities are shortened before the delivery itself is rejected.
// A shortened list keeps a truncation marker. When not even that marker fits,
// the field is left absent and later reads as unknown.
func fitEmission(emission Emission) (Emission, []byte, error) {
	try := func(entries *[]EmittedEntry) ([]byte, bool, error) {
		emission.Entries = entries
		payload, err := canonicalEmission(emission)
		if err != nil {
			return nil, false, fmt.Errorf("invalid delivery")
		}
		return payload, fitsEmission(payload), nil
	}
	payload, ok, err := try(emission.Entries)
	if err != nil {
		return Emission{}, nil, err
	}
	if ok {
		return emission, payload, nil
	}
	if emission.Entries == nil || len(*emission.Entries) == 0 {
		return Emission{}, nil, fmt.Errorf("invalid delivery")
	}
	kept := make([]EmittedEntry, 0, len(*emission.Entries))
	for _, item := range *emission.Entries {
		if item.Reason != "truncated" {
			kept = append(kept, item)
		}
	}
	for dropped := 1; dropped <= len(kept); dropped++ {
		base := append([]EmittedEntry(nil), kept[:len(kept)-dropped]...)
		trial := append(base, EmittedEntry{Role: "omission", Reason: "truncated"})
		payload, ok, err = try(&trial)
		if err != nil {
			return Emission{}, nil, err
		}
		if ok {
			return emission, payload, nil
		}
	}
	payload, ok, err = try(nil)
	if err != nil || !ok {
		return Emission{}, nil, fmt.Errorf("invalid delivery")
	}
	return emission, payload, nil
}

func fitsEmission(payload []byte) bool {
	return len(payload)+1 <= maxEmissionFileBytes
}

func canonicalEmission(emission Emission) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(emission); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func replaceFile(root *os.Root, name string, payload []byte) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("delivery storage failed")
	}
	temp := ".tmp-" + hex.EncodeToString(nonce[:])
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("delivery storage failed")
	}
	_, writeErr := file.Write(payload)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		root.Remove(temp)
		return fmt.Errorf("delivery storage failed")
	}
	if err = root.Rename(temp, name); err != nil {
		root.Remove(temp)
		return fmt.Errorf("delivery storage failed")
	}
	return nil
}

// ReadEmission returns a previously stored delivery record.
func ReadEmission(bundleDir, eventID string) (Emission, error) {
	if !validID(eventID) {
		return Emission{}, fmt.Errorf("invalid delivery")
	}
	root, err := os.OpenRoot(bundleDir)
	if err != nil {
		return Emission{}, err
	}
	defer root.Close()
	events, err := root.OpenRoot("events")
	if err != nil {
		return Emission{}, err
	}
	defer events.Close()
	delivery, err := events.OpenRoot("delivery")
	if err != nil {
		return Emission{}, err
	}
	defer delivery.Close()
	file, err := delivery.Open(eventID + ".json")
	if err != nil {
		return Emission{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxEmissionBytes+4096))
	if err != nil {
		return Emission{}, err
	}
	var emission Emission
	if err = strictjson.Decode(bytes.TrimSpace(data), &emission); err != nil {
		return Emission{}, fmt.Errorf("invalid delivery")
	}
	if emission.EventID != eventID || emission.Text == "" || emission.Selected < 0 || emission.Emitted < 0 || emission.Emitted > emission.Selected || emission.Acknowledged != "unknown" || emission.Used != "unknown" || (emission.ElapsedMs != nil && (*emission.ElapsedMs < 0 || *emission.ElapsedMs > 600000)) {
		return Emission{}, fmt.Errorf("invalid delivery")
	}
	if emission.Omissions != nil {
		for _, item := range *emission.Omissions {
			if err = validOmission(item); err != nil {
				return Emission{}, err
			}
		}
	}
	if emission.Entries != nil {
		for _, item := range *emission.Entries {
			if err = validEmittedEntry(item); err != nil {
				return Emission{}, err
			}
		}
	}
	return emission, nil
}
