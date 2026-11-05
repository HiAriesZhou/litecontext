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

	"github.com/HiAriesZhou/litecontext/internal/strictjson"
)

const maxEmissionBytes = 64 << 10

// Emission is the delivery record for one host injection.
// Emitted is the number of entries returned. Text is the rendered context.
type Emission struct {
	EventID      string `json:"eventId"`
	Text         string `json:"text"`
	Selected     int    `json:"selected"`
	Emitted      int    `json:"emitted"`
	Acknowledged string `json:"acknowledged"`
	Used         string `json:"used"`
	ElapsedMs    *int   `json:"elapsedMs,omitempty"`
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
	if err := plainText(input.Text, maxEmissionBytes); err != nil {
		return fmt.Errorf("invalid delivery")
	}
	payload, err := canonicalEmission(Emission{
		EventID: eventID, Text: input.Text, Selected: input.Selected, Emitted: input.Emitted,
		Acknowledged: "unknown", Used: "unknown", ElapsedMs: input.ElapsedMs,
	})
	if err != nil {
		return fmt.Errorf("invalid delivery")
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
	return emission, nil
}
