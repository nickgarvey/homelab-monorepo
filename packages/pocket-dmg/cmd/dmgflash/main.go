// dmgflash flashes the Pocket DMG controller MCU with AYANEO's USB update
// protocol: serial "enter bootloader" frame, then 58-byte UpdatePack packets to
// the bootloader's HID interface (0483:5750), then UpdateQuit. Runs on the device as root.
package main

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	serialPort  = "/dev/ttyHS4"
	blHIDID     = "0003:00000483:00005750"
	chunk       = 58
	typePack    = 2
	typeQuit    = 3
	replyWait   = 500 * time.Millisecond
	replyTries  = 30
	enterBLWait = 20 * time.Second
)

// enterBootloader is E7 + "0000000000000007" + sum8 + ED, as HandlerUpdateInfoFragment sends it.
var enterBootloader = []byte{0xe7, 0, 0, 0, 0, 0, 0, 0, 0x07, 0x07, 0xed}

func packets(img []byte) [][]byte {
	var out [][]byte
	var idx uint16
	for off := 0; off < len(img); off += chunk {
		n := min(chunk, len(img)-off)
		p := make([]byte, 64)
		p[2] = byte(n)
		p[3] = typePack
		binary.LittleEndian.PutUint16(p[4:], idx)
		copy(p[6:], img[off:off+n])
		var sum uint16
		for _, b := range p[6 : 6+n] {
			sum += uint16(b)
		}
		binary.LittleEndian.PutUint16(p[0:], sum)
		out = append(out, p)
		idx++
	}
	return out
}

func quitPacket() []byte {
	p := make([]byte, 64)
	p[3] = typeQuit
	return p
}

func findHidraw(id string) (string, error) {
	ms, _ := filepath.Glob("/sys/class/hidraw/hidraw*")
	for _, m := range ms {
		ue, err := os.ReadFile(filepath.Join(m, "device", "uevent"))
		if err == nil && strings.Contains(string(ue), "HID_ID="+id) {
			return "/dev/" + filepath.Base(m), nil
		}
	}
	return "", errors.New("not found")
}

type hid struct{ f *os.File }

func openHID(path string) (*hid, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return &hid{os.NewFile(uintptr(fd), path)}, nil
}

// drain discards stale input reports.
func (h *hid) drain() {
	buf := make([]byte, 64)
	for {
		if n, err := syscall.Read(int(h.f.Fd()), buf); n <= 0 || err != nil {
			return
		}
	}
}

// xfer writes one 64-byte report (report ID 0 prefix) and waits for a reply,
// like FirmwareUpdateByUsb.sendData: up to 30 reads 500 ms apart.
func (h *hid) xfer(p []byte) ([]byte, error) {
	if _, err := syscall.Write(int(h.f.Fd()), append([]byte{0}, p...)); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	buf := make([]byte, 64)
	for try := 0; try < replyTries; try++ {
		deadline := time.Now().Add(replyWait)
		for time.Now().Before(deadline) {
			n, err := syscall.Read(int(h.f.Fd()), buf)
			if n > 0 {
				return buf[:n], nil
			}
			if err != nil && err != syscall.EAGAIN {
				return nil, fmt.Errorf("read: %w", err)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	return nil, errors.New("no reply")
}

func sendSerial(frame []byte) (string, error) {
	fd, err := syscall.Open(serialPort, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer syscall.Close(fd)
	if _, err := syscall.Write(fd, frame); err != nil {
		return "", err
	}
	var got []byte
	buf := make([]byte, 64)
	for end := time.Now().Add(time.Second); time.Now().Before(end); {
		if n, _ := syscall.Read(fd, buf); n > 0 {
			got = append(got, buf[:n]...)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return hex.EncodeToString(got), nil
}

func main() {
	dry := flag.Bool("n", false, "dry run: print packets, touch nothing")
	enter := flag.Bool("enter", true, "send the enter-bootloader serial frame first")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: dmgflash [-n] [-enter=false] <image>")
		os.Exit(2)
	}
	img, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	pk := packets(img)
	fmt.Printf("image %d bytes, %d packets\n", len(img), len(pk))
	if *dry {
		for _, i := range []int{0, 1, len(pk) - 1} {
			fmt.Printf("pkt %d: %x\n", i, pk[i])
		}
		fmt.Printf("quit: %x\n", quitPacket())
		return
	}
	if *enter {
		if p, err := findHidraw(blHIDID); err == nil {
			fmt.Println("bootloader already present at", p)
		} else {
			r, err := sendSerial(enterBootloader)
			fmt.Printf("enter-bootloader sent, serial reply %q err=%v\n", r, err)
		}
	}
	var path string
	for end := time.Now().Add(enterBLWait); ; {
		if path, err = findHidraw(blHIDID); err == nil {
			break
		}
		if time.Now().After(end) {
			fmt.Fprintln(os.Stderr, "bootloader HID 0483:5750 did not appear")
			os.Exit(1)
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Println("bootloader at", path)
	time.Sleep(300 * time.Millisecond)
	h, err := openHID(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	h.drain()
	start := time.Now()
	for i, p := range pk {
		r, err := h.xfer(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "packet %d/%d: %v\n", i, len(pk), err)
			os.Exit(1)
		}
		if i < 3 || i == len(pk)-1 || i%200 == 0 {
			fmt.Printf("packet %d/%d reply %x\n", i, len(pk), r[:min(16, len(r))])
		}
	}
	r, err := h.xfer(quitPacket())
	fmt.Printf("quit reply %x err=%v (%.1fs)\n", r, err, time.Since(start).Seconds())
}
