package cart

import (
	"errors"
	"testing"
	"time"
)

var calls int

func TestAddItem(t *testing.T) {}

func TestCheckoutSlow(t *testing.T) { time.Sleep(2300 * time.Millisecond) }

func TestCacheWarmup(t *testing.T) {
	calls++
	if calls%3 == 1 {
		t.Fatalf("cache warmup: context deadline exceeded after 50ms")
	}
}

func TestParsePrice(t *testing.T) {
	t.Run("valid", func(t *testing.T) {})
	t.Run("negative", func(t *testing.T) {
		t.Errorf("ParsePrice(\"-1\") = 1, expected error but got nil")
	})
}

func TestInventoryClient(t *testing.T) {
	t.Fatal(errors.New("dial tcp 127.0.0.1:5432: connect: connection refused"))
}

func TestLegacyDiscount(t *testing.T) { t.Skip("legacy pricing disabled") }
