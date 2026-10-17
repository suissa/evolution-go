// patch-whatsmeow applies the minimal upstream whatsmeow changes needed to
// receive WhatsApp live-location updates. The source is patched in the Go module
// cache, leaving the application's go.mod/go.sum pinned to the upstream version.
//
// Use -restore before go mod verify to restore the cached upstream source.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const modulePath = "go.mau.fi/whatsmeow"

const subscribeSource = `// Copyright (c) 2026 Evolution GO contributors
// SPDX-License-Identifier: MPL-2.0

package whatsmeow

import (
	"context"
	"fmt"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

// SubscribeLiveLocation subscribes this linked device to live-location updates
// from target. WhatsApp clients refresh this subscription periodically.
func (cli *Client) SubscribeLiveLocation(ctx context.Context, target types.JID) error {
	if cli == nil {
		return ErrClientIsNil
	}
	if target.IsEmpty() {
		return fmt.Errorf("live-location target JID is empty")
	}

	requestID := cli.generateRequestID()
	waiter := cli.waitResponse(requestID)
	node := waBinary.Node{
		Tag: "iq",
		Attrs: waBinary.Attrs{
			"id":     requestID,
			"xmlns":  "location",
			"type":   "get",
			"to":     "s.whatsapp.net",
			"target": target.String(),
		},
		Content: []waBinary.Node{{
			Tag: "subscribe",
			Attrs: waBinary.Attrs{
				"participants": "true",
			},
		}},
	}
	if _, err := cli.sendNodeAndGetData(ctx, node); err != nil {
		cli.cancelResponse(requestID, waiter)
		return fmt.Errorf("send live-location subscription: %w", err)
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case response := <-waiter:
		if response == nil {
			return fmt.Errorf("empty live-location subscription response")
		}
		if response.Tag != "iq" {
			return fmt.Errorf("unexpected live-location subscription response node %q", response.Tag)
		}
		responseType, _ := response.Attrs["type"].(string)
		if responseType == "error" {
			return parseIQError(response)
		}
		if responseType != "result" {
			return fmt.Errorf("unexpected live-location subscription IQ type %q", responseType)
		}
		return nil
	case <-ctx.Done():
		cli.cancelResponse(requestID, waiter)
		return ctx.Err()
	case <-timer.C:
		cli.cancelResponse(requestID, waiter)
		return ErrIQTimedOut
	}
}
`

const liveLocationTestSource = `package whatsmeow

import "testing"

func TestIsFastRatchetSenderKeyMessage(t *testing.T) {
	for _, encType := range []string{"frskmsg", "fskmsg"} {
		if !isFastRatchetSenderKeyMessage(encType) {
			t.Errorf("%q must be recognized as a fast-ratchet sender-key message", encType)
		}
	}
	for _, encType := range []string{"msg", "pkmsg", "skmsg", "unknown"} {
		if isFastRatchetSenderKeyMessage(encType) {
			t.Errorf("%q must not be recognized as a fast-ratchet sender-key message", encType)
		}
	}
}
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "patch-whatsmeow:", err)
		os.Exit(1)
	}
}

func run() error {
	restore := len(os.Args) > 1 && os.Args[1] == "-restore"
	if len(os.Args) > 2 || (len(os.Args) == 2 && !restore) {
		return errors.New("usage: go run ./scripts/patch-whatsmeow [-restore]")
	}

	moduleDirBytes, err := exec.Command("go", "list", "-m", "-f={{.Dir}}", modulePath).Output()
	if err != nil {
		return fmt.Errorf("locate %s module: %w", modulePath, err)
	}
	moduleDir := strings.TrimSpace(string(moduleDirBytes))
	if moduleDir == "" {
		return fmt.Errorf("go list returned an empty module directory for %s", modulePath)
	}

	sourcePath := filepath.Join(moduleDir, "message.go")
	locationPath := filepath.Join(moduleDir, "location.go")
	testPath := filepath.Join(moduleDir, "location_live_test.go")
	backupDir, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("find user cache directory: %w", err)
	}
	backupPath := filepath.Join(backupDir, "evolution-go", "whatsmeow", filepath.Base(moduleDir), "message.go.original")

	if restore {
		if _, err := os.Stat(backupPath); err == nil {
			original, readErr := os.ReadFile(backupPath)
			if readErr != nil {
				return fmt.Errorf("read upstream message.go backup: %w", readErr)
			}
			if err = makeWritable(moduleDir, sourcePath); err != nil {
				return err
			}
			if err = os.WriteFile(sourcePath, original, 0o644); err != nil {
				return fmt.Errorf("restore upstream message.go: %w", err)
			}
			if err = os.Remove(locationPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove live-location extension during restore: %w", err)
			}
			if err = os.Remove(testPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove live-location tests during restore: %w", err)
			}
			fmt.Printf("Restored upstream %s source in %s
", modulePath, moduleDir)
			return nil
		} else if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("No Evolution GO whatsmeow patch found in %s; nothing to restore
", moduleDir)
			return nil
		} else {
			return fmt.Errorf("inspect upstream backup: %w", err)
		}
	}

	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read whatsmeow message.go: %w", err)
	}
	if strings.Contains(string(source), "func isFastRatchetSenderKeyMessage(") {
		if err := writeExtensionFiles(moduleDir, locationPath, testPath); err != nil {
			return err
		}
		fmt.Printf("%s already supports live-location updates
", modulePath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
		return fmt.Errorf("create patch backup directory: %w", err)
	}
	if _, err := os.Stat(backupPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(backupPath, source, 0o644); err != nil {
			return fmt.Errorf("save original whatsmeow message.go: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect original source backup: %w", err)
	}

	patched := string(source)
	replacements := []struct {
		name string
		old  string
		new  string
	}{
		{
			name: "sort import",
			old: `	"runtime/debug"
	"strconv"`,
			new: `	"runtime/debug"
	"sort"
	"strconv"`,
		},
		{
			name: "decryption child ordering",
			old: `	children := node.GetChildren()
	cli.Log.Debugf("Decrypting message from %s", info.SourceString())`,
			new: `	children := append([]waBinary.Node(nil), node.GetChildren()...)
	isGroupEncryptedNode := func(child waBinary.Node) bool {
		if child.Tag != "enc" {
			return false
		}
		encType, _ := child.Attrs["type"].(string)
		return encType == "skmsg" || isFastRatchetSenderKeyMessage(encType)
	}
	// Sender-key distribution can be wrapped in a normal msg/pkmsg in the
	// same stanza. Install it before attempting to decrypt frskmsg updates.
	sort.SliceStable(children, func(i, j int) bool {
		return !isGroupEncryptedNode(children[i]) && isGroupEncryptedNode(children[j])
	})
	cli.Log.Debugf("Decrypting message from %s", info.SourceString())`,
		},
		{
			name: "fast-ratchet decryption branch",
			old: `		} else if info.IsGroup && encType == "skmsg" {
			decrypted, ciphertextHash, err = cli.decryptGroupMsg(ctx, &child, senderEncryptionJID, info.Chat, info.Timestamp)
		} else if encType == "msmsg" && info.Sender.IsBot() {`,
			new: `		} else if (info.IsGroup && encType == "skmsg") || isFastRatchetSenderKeyMessage(encType) {
			decrypted, ciphertextHash, err = cli.decryptGroupMsg(ctx, &child, senderEncryptionJID, info.Chat, info.Timestamp)
		} else if encType == "msmsg" && info.Sender.IsBot() {`,
		},
		{
			name: "fast-ratchet sender-key distribution",
			old: `	if msg.GetSenderKeyDistributionMessage() != nil {
		if !info.IsGroup {
			cli.Log.Warnf("Got sender key distribution message in non-group chat from %s", info.Sender)
		} else {
			encryptionIdentity := info.Sender
			if encryptionIdentity.Server == types.DefaultUserServer && info.SenderAlt.Server == types.HiddenUserServer {
				encryptionIdentity = info.SenderAlt
			}
			cli.handleSenderKeyDistributionMessage(ctx, info.Chat, encryptionIdentity, msg.SenderKeyDistributionMessage.AxolotlSenderKeyDistributionMessage)
		}
	}`,
			new: `	handleDistribution := func(distribution *waE2E.SenderKeyDistributionMessage, allowNonGroup bool) {
		if distribution == nil {
			return
		}
		if !info.IsGroup && !allowNonGroup {
			cli.Log.Warnf("Got sender key distribution message in non-group chat from %s", info.Sender)
			return
		}
		encryptionIdentity := info.Sender
		if encryptionIdentity.Server == types.DefaultUserServer && info.SenderAlt.Server == types.HiddenUserServer {
			encryptionIdentity = info.SenderAlt
		}
		cli.handleSenderKeyDistributionMessage(ctx, info.Chat, encryptionIdentity, distribution.GetAxolotlSenderKeyDistributionMessage())
	}
	handleDistribution(msg.GetSenderKeyDistributionMessage(), false)
	// Live-location shares use the fast-ratchet distribution field even in a
	// direct chat, so it must be installed independently of info.IsGroup.
	handleDistribution(msg.GetFastRatchetKeySenderKeyDistributionMessage(), true)`,
		},
	}
	for _, replacement := range replacements {
		if strings.Count(patched, replacement.old) != 1 {
			return fmt.Errorf("upstream whatsmeow source mismatch at %q (expected one match)", replacement.name)
		}
		patched = strings.Replace(patched, replacement.old, replacement.new, 1)
	}
	patched += "

" + `// isFastRatchetSenderKeyMessage recognizes encrypted envelopes used for live-location updates.
// Both spellings have been observed in WhatsApp traffic.
func isFastRatchetSenderKeyMessage(encType string) bool {
	return encType == "frskmsg" || encType == "fskmsg"
}
`

	if err := makeWritable(moduleDir, sourcePath); err != nil {
		return err
	}
	if err := os.WriteFile(sourcePath, []byte(patched), 0o644); err != nil {
		return fmt.Errorf("write patched whatsmeow message.go: %w", err)
	}
	if err := writeExtensionFiles(moduleDir, locationPath, testPath); err != nil {
		return err
	}
	fmt.Printf("Patched %s for encrypted live-location updates in %s
", modulePath, moduleDir)
	return nil
}

func makeWritable(moduleDir, sourcePath string) error {
	if err := os.Chmod(moduleDir, 0o755); err != nil {
		return fmt.Errorf("make whatsmeow module directory writable: %w", err)
	}
	if err := os.Chmod(sourcePath, 0o644); err != nil {
		return fmt.Errorf("make whatsmeow source writable: %w", err)
	}
	return nil
}

func writeExtensionFiles(moduleDir, locationPath, testPath string) error {
	if err := os.Chmod(moduleDir, 0o755); err != nil {
		return fmt.Errorf("make whatsmeow module directory writable: %w", err)
	}
	if err := os.WriteFile(locationPath, []byte(subscribeSource), 0o644); err != nil {
		return fmt.Errorf("write whatsmeow live-location subscription method: %w", err)
	}
	if err := os.WriteFile(testPath, []byte(liveLocationTestSource), 0o644); err != nil {
		return fmt.Errorf("write whatsmeow live-location tests: %w", err)
	}
	return nil
}

