package backup

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// The archive is sequential so neither the browser nor the server needs to
// collect a large account in memory. Names carry sequence numbers rather
// than database IDs. Only IDs created within this import can be continued.
type archiveCounts struct {
	Threads  int `json:"threads"`
	Parts    int `json:"parts"`
	Messages int `json:"messages"`
}

func partName(thread, part int) string { return fmt.Sprintf("threads/%08d/%08d.json", thread, part) }

func writeEntry(w *tar.Writer, name string, payload []byte) error {
	if len(payload) > MaxDocumentBytes {
		return ErrTooLarge
	}
	if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(payload))}); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func (s *Service) exportParts(ctx context.Context, account user.User, out io.Writer, pageSize int) error {
	header, err := s.exportHeader(ctx, account)
	if err != nil {
		return err
	}
	w := tar.NewWriter(out)
	h, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if err = writeEntry(w, "account.json", h); err != nil {
		return err
	}
	var counts archiveCounts
	cursor := ""
	for {
		rows, err := s.conversations.ListForExport(ctx, account.ID, cursor, exportPageSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, c := range rows {
			counts.Threads++
			after, part := 0, 0
			for {
				page, err := s.conversations.MessagePage(ctx, nil, account.ID, c.ID, after, pageSize)
				if err != nil {
					return err
				}
				if len(page) == 0 && part > 0 {
					break
				}
				thread := threadOf(c)
				thread.Messages = make([]Turn, 0, len(page))
				for _, m := range page {
					thread.Messages = append(thread.Messages, turnOf(m))
					after = m.Seq
				}
				if err := writeThreadParts(w, thread, counts.Threads, &part); err != nil {
					return err
				}
				counts.Messages += len(page)
				if len(page) == 0 {
					break
				}
			}
			counts.Parts += part
			cursor = c.ID
		}
	}
	footer, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	if err := writeEntry(w, "complete.json", footer); err != nil {
		return err
	}
	return w.Close()
}

type threadPart struct {
	Thread
	ContentBytes   int `json:"content_bytes,omitempty"`
	ReasoningBytes int `json:"reasoning_bytes,omitempty"`
}

// JSON can expand a byte into a six-byte escape. Large fields instead travel
// as raw UTF-8 entries so even a maximum-size model response can round-trip.
func writeThreadParts(w *tar.Writer, thread Thread, number int, part *int) error {
	size := 0
	for _, m := range thread.Messages {
		size += len(m.Content) + len(m.Reasoning)
	}
	if size > MaxDocumentBytes/8 && len(thread.Messages) > 1 {
		turns := thread.Messages
		mid := len(turns) / 2
		thread.Messages = turns[:mid]
		if err := writeThreadParts(w, thread, number, part); err != nil {
			return err
		}
		thread.Messages = turns[mid:]
		return writeThreadParts(w, thread, number, part)
	}
	name := partName(number, *part)
	*part++
	payload := threadPart{Thread: thread}
	var content, reasoning string
	if size > MaxDocumentBytes/8 {
		m := thread.Messages[0]
		content, reasoning = m.Content, m.Reasoning
		m.Content, m.Reasoning = "", ""
		payload.Messages = []Turn{m}
		payload.ContentBytes, payload.ReasoningBytes = len(content), len(reasoning)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := writeEntry(w, name, raw); err != nil {
		return err
	}
	if err := writeText(w, name+".content", content); err != nil {
		return err
	}
	return writeText(w, name+".reasoning", reasoning)
}

func writeText(w *tar.Writer, name, value string) error {
	for n := 0; len(value) > 0; n++ {
		size := min(len(value), MaxDocumentBytes)
		if err := w.WriteHeader(&tar.Header{Name: fmt.Sprintf("%s.%d", name, n), Mode: 0600, Size: int64(size)}); err != nil {
			return err
		}
		if _, err := io.WriteString(w, value[:size]); err != nil {
			return err
		}
		value = value[size:]
	}
	return nil
}

func readText(r *tar.Reader, name string, size int) (string, error) {
	var value strings.Builder
	for n := 0; size > 0; n++ {
		h, err := r.Next()
		count := min(size, MaxDocumentBytes)
		if err != nil || h.Typeflag != tar.TypeReg || h.Name != fmt.Sprintf("%s.%d", name, n) || h.Size != int64(count) {
			return "", ErrWrongFormat
		}
		if _, err := io.CopyN(&value, r, int64(count)); err != nil {
			return "", err
		}
		size -= count
	}
	if !utf8.ValidString(value.String()) {
		return "", ErrWrongFormat
	}
	return value.String(), nil
}

func readEntry(r *tar.Reader, h *tar.Header, dst any) error {
	if h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > MaxDocumentBytes {
		return ErrWrongFormat
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if int64(len(raw)) != h.Size || json.Unmarshal(raw, dst) != nil {
		return ErrWrongFormat
	}
	return nil
}

func (s *Service) importParts(ctx context.Context, account user.User, body io.Reader) (Result, error) {
	r := tar.NewReader(body)
	h, err := r.Next()
	if err != nil || h.Name != "account.json" {
		return Result{}, ErrWrongFormat
	}
	var header Document
	if err = readEntry(r, h, &header); err != nil {
		return Result{}, err
	}
	if len(header.Conversations) != 0 {
		return Result{}, ErrWrongFormat
	}
	refs := map[string]string{}
	result, err := s.importDocument(ctx, account, header, refs)
	if err != nil {
		return result, err
	}
	stored, err := s.storedChars(ctx, account.ID)
	if err != nil {
		return result, err
	}
	tally := &charTally{stored: stored, ceiling: s.charCeiling()}
	counts := archiveCounts{}
	part := 0
	target := ""
	for {
		h, err = r.Next()
		if err != nil {
			return result, ErrWrongFormat
		}
		if h.Name == "complete.json" {
			var declared archiveCounts
			if err := readEntry(r, h, &declared); err != nil {
				return result, err
			}
			if declared != counts {
				return result, ErrWrongFormat
			}
			if _, err := r.Next(); !errors.Is(err, io.EOF) {
				return result, ErrWrongFormat
			}
			return result, nil
		}
		first := h.Name == partName(counts.Threads+1, 0)
		if first {
			if counts.Threads >= MaxStoredMessages {
				return result, ErrTooLarge
			}
			counts.Threads++
			part = 0
			target = ""
		}
		if counts.Threads == 0 || h.Name != partName(counts.Threads, part) {
			return result, ErrWrongFormat
		}
		var payload threadPart
		if err := readEntry(r, h, &payload); err != nil {
			return result, err
		}
		thread := payload.Thread
		if payload.ContentBytes != 0 || payload.ReasoningBytes != 0 {
			if len(thread.Messages) != 1 || payload.ContentBytes < 0 || payload.ReasoningBytes < 0 || payload.ContentBytes > adapter.MaxResponseBytes || payload.ReasoningBytes > adapter.MaxResponseBytes-payload.ContentBytes {
				return result, ErrWrongFormat
			}
			m := &thread.Messages[0]
			if m.Content != "" || m.Reasoning != "" {
				return result, ErrWrongFormat
			}
			m.Content, err = readText(r, h.Name+".content", payload.ContentBytes)
			if err != nil {
				return result, err
			}
			m.Reasoning, err = readText(r, h.Name+".reasoning", payload.ReasoningBytes)
			if err != nil {
				return result, err
			}
		}
		if !first && len(thread.Messages) == 0 {
			return result, ErrWrongFormat
		}
		if len(thread.Messages) > MaxMessagesPerImport {
			return result, ErrTooLarge
		}
		if !validTimestamp(thread.CreatedAt) || !validTimestamp(thread.UpdatedAt) || hasNUL(Document{Conversations: []Thread{thread}}) {
			return result, ErrWrongFormat
		}
		for _, m := range thread.Messages {
			if !validTimestamp(m.CreatedAt) || (m.Role != string(conversation.RoleUser) && m.Role != string(conversation.RoleAssistant)) {
				return result, ErrWrongFormat
			}
		}
		if thread.ProjectID != "" {
			mapped, ok := refs[thread.ProjectID]
			if !ok {
				return result, ErrWrongFormat
			}
			thread.ProjectID = mapped
		}
		written, unpinned, err := s.importPart(ctx, account, thread, tally, &target, true)
		if err != nil {
			return result, err
		}
		if first {
			result.Conversations++
		}
		if unpinned {
			result.Unpinned++
		}
		result.Messages += written
		counts.Messages += written
		counts.Parts++
		part++
	}
}
