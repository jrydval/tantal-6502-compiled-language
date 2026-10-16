package cpu

import (
	"os"
	"testing"
)

type ram [65536]byte

func (r *ram) Read(a uint16) byte     { return r[a] }
func (r *ram) Write(a uint16, v byte) { r[a] = v }

func TestDormann(t *testing.T) {
	data, err := os.ReadFile(os.Getenv("DORMANN"))
	if err != nil {
		t.Skip("no image")
	}
	var m ram
	copy(m[:], data)
	c := New(&m)
	c.PC = 0x0400
	for i := 0; i < 200000000; i++ {
		pc := c.PC
		if _, err := c.Step(); err != nil {
			t.Fatal(err)
		}
		if c.PC == pc {
			if pc == 0x3469 {
				t.Logf("OK, cycles %d", c.Cycles)
				return
			}
			t.Fatalf("trap at $%04X", pc)
		}
	}
	t.Fatal("timeout")
}
