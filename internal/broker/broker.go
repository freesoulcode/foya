// Package broker 提供类型安全的内核事件总线。
//
// Broker 把内核事件扇出给多个订阅者(多客户端订阅同一会话)。
// 采用两级投递语义:
//   - Publish:有损,订阅者 buffer 满即丢弃(适合高频 token 增量)。
//   - PublishMustDeliver:必达,有界阻塞 + 超时(适合工具结果、
//     回合结束、审批请求等不能被静默合并的终止事件)。
package broker

import (
	"context"
	"sync"
	"time"
)

// 每个订阅者的缓冲区大小。满后 Publish 丢弃(有损),PublishMustDeliver 阻塞。
const subscriberBuffer = 256

// 必达投递的阻塞超时。
const mustDeliverTimeout = 50 * time.Millisecond

// Broker 是按 topic 扇出的泛型发布订阅总线。
type Broker[T any] struct {
	mu     sync.RWMutex
	nextID uint64
	subs   map[string]map[uint64]chan T // topic -> subscriberID -> chan
}

// New 创建一个 Broker。
func New[T any]() *Broker[T] {
	return &Broker[T]{subs: make(map[string]map[uint64]chan T)}
}

// Subscribe 订阅某 topic,返回只读通道;ctx 取消时自动注销并关闭通道。
func (b *Broker[T]) Subscribe(ctx context.Context, topic string) <-chan T {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	ch := make(chan T, subscriberBuffer)
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[uint64]chan T)
	}
	b.subs[topic][id] = ch
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		if m := b.subs[topic]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(b.subs, topic)
			}
		}
		close(ch)
		b.mu.Unlock()
	}()

	return ch
}

// Publish 有损投递:订阅者 buffer 满即丢弃。适合高频 token 增量。
func (b *Broker[T]) Publish(topic string, ev T) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs[topic] {
		select {
		case ch <- ev:
		default: // buffer 满,丢弃
		}
	}
}

// PublishMustDeliver 必达投递:有界阻塞 + 超时。适合终止类事件。
func (b *Broker[T]) PublishMustDeliver(ctx context.Context, topic string, ev T) error {
	b.mu.RLock()
	chans := make([]chan T, 0, len(b.subs[topic]))
	for _, ch := range b.subs[topic] {
		chans = append(chans, ch)
	}
	b.mu.RUnlock()

	for _, ch := range chans {
		select {
		case ch <- ev: // 先尝试非阻塞
		default:
			// buffer 满,有界阻塞等待
			timer := time.NewTimer(mustDeliverTimeout)
			select {
			case ch <- ev:
				timer.Stop()
			case <-timer.C:
				// 超时:该订阅者消费过慢,跳过(不拖累其它订阅者)
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
	}
	return nil
}
