package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// ErrInvalidHex indicates that a hex string cannot be converted to an ObjectID.
var ErrInvalidHex = errors.New("the provided hex string is not a valid ObjectID")

// ObjectID is a 12-byte unique identifier, compatible with MongoDB BSON ObjectID format.
// Layout: 4 bytes timestamp (big-endian) + 5 bytes process-unique random + 2 bytes counter.
type ObjectID [12]byte

// NilObjectID is the zero value for ObjectID.
var NilObjectID ObjectID

var objectIDCounter = readRandomUint32()
var processUnique = processUniqueBytes()

// NewObjectID generates a new ObjectID using the current time.
func NewObjectID() ObjectID {
	return NewObjectIDFromTimestamp(time.Now())
}

// NewObjectIDFromTimestamp generates a new ObjectID based on the given time.
func NewObjectIDFromTimestamp(timestamp time.Time) ObjectID {
	var b [12]byte

	b[0] = byte(timestamp.Unix() >> 24)
	b[1] = byte(timestamp.Unix() >> 16)
	b[2] = byte(timestamp.Unix() >> 8)
	b[3] = byte(timestamp.Unix())

	copy(b[4:9], processUnique[:])

	counter := atomic.AddUint32(&objectIDCounter, 1)
	b[9] = byte(counter >> 16)
	b[10] = byte(counter >> 8)
	b[11] = byte(counter)

	return b
}

// Hex returns the hex encoding of the ObjectID as a 24-character string.
func (id ObjectID) Hex() string {
	var buf [24]byte
	hex.Encode(buf[:], id[:])
	return string(buf[:])
}

// String returns the ObjectID as a string representation.
func (id ObjectID) String() string {
	return fmt.Sprintf("ObjectID(%q)", id.Hex())
}

// IsZero returns true if id is the empty (nil) ObjectID.
func (id ObjectID) IsZero() bool {
	return id == NilObjectID
}

// MarshalJSON returns the ObjectID as a JSON string (hex-encoded).
func (id ObjectID) MarshalJSON() ([]byte, error) {
	return json.Marshal(id.Hex())
}

// UnmarshalJSON populates the ObjectID from a JSON value. Accepts a 24-char hex string,
// an empty string (decodes as NilObjectID), a 12-byte raw array, or extended JSON {"$oid": "..."}.
func (id *ObjectID) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}

	switch len(b) {
	case 12: // raw 12 bytes
		copy(id[:], b)
		return nil
	default:
		var res interface{}
		if err := json.Unmarshal(b, &res); err != nil {
			return err
		}

		var str string
		switch v := res.(type) {
		case string:
			str = v
		case map[string]interface{}:
			oid, ok := v["$oid"]
			if !ok {
				return errors.New("not an extended JSON ObjectID")
			}
			str, ok = oid.(string)
			if !ok {
				return errors.New("not an extended JSON ObjectID")
			}
		default:
			return errors.New("not an extended JSON ObjectID")
		}

		if len(str) == 0 {
			copy(id[:], NilObjectID[:])
			return nil
		}

		if len(str) != 24 {
			return fmt.Errorf("cannot unmarshal into an ObjectID, the length must be 24 but it is %d", len(str))
		}

		_, err := hex.Decode(id[:], []byte(str))
		return err
	}
}

// MarshalText returns the ObjectID as UTF-8-encoded text (hex string).
func (id ObjectID) MarshalText() ([]byte, error) {
	return []byte(id.Hex()), nil
}

// UnmarshalText populates the ObjectID from UTF-8 text (hex string).
func (id *ObjectID) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		copy(id[:], NilObjectID[:])
		return nil
	}
	if len(b) != 24 {
		return ErrInvalidHex
	}
	_, err := hex.Decode(id[:], b)
	return err
}

// ObjectIDFromHex creates a new ObjectID from a hex string.
func ObjectIDFromHex(s string) (ObjectID, error) {
	if len(s) != 24 {
		return NilObjectID, ErrInvalidHex
	}
	var oid [12]byte
	_, err := hex.Decode(oid[:], []byte(s))
	return oid, err
}

func processUniqueBytes() [5]byte {
	var b [5]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		panic(fmt.Errorf("cannot initialize objectid package with crypto.rand.Reader: %w", err))
	}
	return b
}

func readRandomUint32() uint32 {
	var b [4]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		panic(fmt.Errorf("cannot initialize objectid package with crypto.rand.Reader: %w", err))
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}