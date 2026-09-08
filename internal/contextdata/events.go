package contextdata

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func (s *Store) Replay(after uint64) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Event, 0)
	for _, ev := range s.events {
		if ev.Seq > after {
			items = append(items, ev)
		}
	}
	return items
}

func (s *Store) Subscribe(ctx context.Context) <-chan Event {
	s.mu.Lock()
	id := s.nextSubID
	s.nextSubID++
	ch := make(chan Event, 64)
	s.subscribers[id] = ch
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.subscribers, id)
		close(ch)
		s.mu.Unlock()
	}()
	return ch
}

func (s *Store) appendMemoryEventLocked(ev Event) error {
	diskEvent := ev
	diskEvent.Memory = nil
	if ev.Memory != nil {
		diskEvent.DeletedID = ev.Memory.ID
	}
	return s.appendEventLocked(diskEvent, ev)
}

func (s *Store) appendRuleEventLocked(ev Event) error {
	diskEvent := ev
	diskEvent.Rule = nil
	if ev.Rule != nil {
		diskEvent.DeletedID = ev.Rule.ID
	}
	return s.appendEventLocked(diskEvent, ev)
}

func (s *Store) appendEventLocked(diskEvent, liveEvent Event) error {
	file, err := os.OpenFile(s.eventPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	data, err := json.Marshal(diskEvent)
	if err == nil {
		data = append(data, '\n')
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	s.seq = liveEvent.Seq
	s.events = append(s.events, liveEvent)
	return nil
}

func (s *Store) publishLocked(ev Event) {
	for _, ch := range s.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (s *Store) loadEvents() error {
	file, err := os.Open(s.eventPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var ev Event
			if err := json.Unmarshal(line, &ev); err != nil {
				if errors.Is(readErr, io.EOF) {
					break
				}
				return fmt.Errorf("restore context event log: %w", err)
			}
			s.applyEvent(ev)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}

func (s *Store) applyEvent(ev Event) {
	if ev.Seq > s.seq {
		s.seq = ev.Seq
	}
	s.events = append(s.events, ev)
}
