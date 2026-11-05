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
)

const maxEmissionBytes = 64 << 10

// Emission is the context text a host injection actually returned.
// Acknowledgement and use stay unknown until a host can prove them.
type Emission struct {
	EventID      string `json:"eventId"`
	Emitted      string `json:"emitted"`
	Acknowledged string `json:"acknowledged"`
	Used         string `json:"used"`
}

// SaveEmission records the text returned to the host for one event.
// A later injection with the same event ID replaces the previous text.
// The file is events/delivery/<eventID>.json, separate from the event record.
func SaveEmission(ctx context.Context, bundleDir, eventID, emitted string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !validID(eventID) || emitted == "" {
		return fmt.Errorf("invalid delivery")
	}
	if err := plainText(emitted, maxEmissionBytes); err != nil {
		return fmt.Errorf("invalid delivery")
	}
	payload, err := canonicalEmission(Emission{
		EventID: eventID, Emitted: emitted, Acknowledged: "unknown", Used: "unknown",
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
	if err = json.Unmarshal(bytes.TrimSpace(data), &emission); err != nil {
		return Emission{}, err
	}
	return emission, nil
}
