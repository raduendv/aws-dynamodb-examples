package util

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"strconv"
	"time"
)

// Sha256Hash hashes supported struct fields in declaration order, skipping hash:"-".
// It rejects nil, non-struct and unsupported fields. Payment intent hashing uses
// its separate length-prefixed exact-decimal contract, not this legacy helper.
func Sha256Hash[T any](v T) (string, error) {
	rv := reflect.ValueOf(v)

	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return "", fmt.Errorf("v is nil")
		}

		rv = rv.Elem()
	}

	if rv.Kind() != reflect.Struct {
		return "", fmt.Errorf("v is not a struct")
	}

	h := sha256.New()

	for c := 0; c < rv.NumField(); c++ {
		f := rv.Field(c)
		if hv, found := rv.Type().Field(c).Tag.Lookup("hash"); found && hv == "-" {
			continue
		}

		switch f.Kind() {
		case reflect.String:
			h.Write([]byte(f.String()))
		case reflect.Slice:
			if f.Type().Elem().Kind() == reflect.Uint8 {
				h.Write(f.Bytes())
			} else {
				return "", fmt.Errorf("unsupported slice type %s", f.Type().Elem().Kind())
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			h.Write([]byte(fmt.Sprintf("%d", f.Int())))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			h.Write([]byte(fmt.Sprintf("%d", f.Uint())))
		case reflect.Float32, reflect.Float64:
			h.Write([]byte(strconv.FormatFloat(f.Float(), 'f', -1, 64)))
		case reflect.Struct:
			if f.Type() == reflect.TypeOf(time.Time{}) {
				t := f.Interface().(time.Time)
				h.Write([]byte(fmt.Sprintf("%d", t.Unix())))
			} else {
				return "", fmt.Errorf("unsupported struct type %s", f.Type())
			}
		default:
			return "", fmt.Errorf("unsupported type %s", f.Kind())
		}
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
