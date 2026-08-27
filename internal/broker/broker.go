// Package broker 提供类型安全的内核事件总线。
//
// Broker 把内核事件扇出给多个订阅者(多客户端订阅同一会话)。
// 采用两级投递语义:
//   - Publish:有损,订阅者 buffer 满即丢弃(适合高频 token 增量)。
//   - PublishMustDeliver:必达,有界阻塞 + 超时(适合工具结果、
//     回合结束、审批请求等不能被静默合并的终止事件)。
package broker

import "context"

// Broker 是泛型发布订阅总线。
type Broker[T any] interface {
	// Subscribe 返回一个只读事件通道;ctx 取消时自动注销并关闭通道。
	Subscribe(ctx context.Context) <-chan T

	// Publish 有损投递:订阅者 buffer 满即丢弃并计数。
	Publish(topic string, ev T)

	// PublishMustDeliver 必达投递:有界阻塞 + 超时。
	PublishMustDeliver(ctx context.Context, topic string, ev T) error
}
