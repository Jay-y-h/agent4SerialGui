package protocol

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// TypeBrowser 是网关转发"浏览器 → 串口"写入指令时使用的消息类型。
const TypeBrowser = "browser"

// Message 是 agent 与网关之间的消息。
//
// 上行（串口 -> 网关）：id、from 均为设备 id，type 为设备名称。
// 下行（网关 -> 串口）：type 必须是 "browser"，to 为目标设备 id；
// 为兼容旧网关，to 为空时会尝试把 id 当作设备 id，仍无法确定且配置了
// 多个设备时拒绝写入。
type Message struct {
	ID        string        `json:"id"`
	Type      string        `json:"type"`
	From      string        `json:"from"`
	To        string        `json:"to,omitempty"`
	Timestamp int64         `json:"timestamp"`
	Payload   SerialPayload `json:"payload"`
}

type SerialPayload struct {
	Data Bytes `json:"data"`
}

// Bytes 在内存里是 []byte，在 JSON 里仍是 [1,2,255] 这样的整数数组，
// 与原协议保持兼容，同时省掉 []byte <-> []int 的手工转换，
// 并在解码时拒绝超出 0-255 的值（原来的 byte(v) 会静默截断）。
type Bytes []byte

func (b Bytes) MarshalJSON() ([]byte, error) {
	out := make([]byte, 0, len(b)*4+2)
	out = append(out, '[')
	for i, v := range b {
		if i > 0 {
			out = append(out, ',')
		}
		out = strconv.AppendUint(out, uint64(v), 10)
	}
	return append(out, ']'), nil
}

func (b *Bytes) UnmarshalJSON(data []byte) error {
	var ints []int
	if err := json.Unmarshal(data, &ints); err != nil {
		return err
	}
	out := make(Bytes, len(ints))
	for i, v := range ints {
		if v < 0 || v > 255 {
			return fmt.Errorf("data[%d]=%d 超出字节范围 0-255", i, v)
		}
		out[i] = byte(v)
	}
	*b = out
	return nil
}
