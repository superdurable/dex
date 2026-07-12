package dex

import (
	"fmt"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

func waitForToProto(condition WaitForCondition, effectiveNowTs int64) (*pb.WaitForCondition, error) {
	if condition == nil {
		return nil, fmt.Errorf("waitForToProto: nil WaitForCondition")
	}
	conditions := waitConditionList(condition)
	if len(conditions) == 0 {
		return nil, fmt.Errorf("waitForToProto: WaitForCondition has no conditions")
	}

	out := &pb.WaitForCondition{
		Conditions: make([]*pb.SingleCondition, 0, len(conditions)),
	}
	switch waitConditionType(condition) {
	case waitTypeAnyOf:
		out.Type = pb.WaitType_WAIT_TYPE_ANY_OF
	case waitTypeAllOf:
		out.Type = pb.WaitType_WAIT_TYPE_ALL_OF
	default:
		return nil, fmt.Errorf("waitForToProto: unknown waitType %d", waitConditionType(condition))
	}

	for index, singleCondition := range conditions {
		switch typed := singleCondition.(type) {
		case timerCondition:
			fireAt := effectiveNowTs + typed.Duration.Milliseconds()
			out.Conditions = append(out.Conditions, &pb.SingleCondition{
				Condition: &pb.SingleCondition_Timer{
					Timer: &pb.TimerCondition{FireAtUnixMs: fireAt},
				},
			})
		case channelCondition:
			min := typed.Min
			if min <= 0 {
				min = 1
			}
			out.Conditions = append(out.Conditions, &pb.SingleCondition{
				Condition: &pb.SingleCondition_Channel{
					Channel: &pb.ChannelCondition{
						ChannelName: typed.ChannelName,
						Min:         int32(min),
						Max:         int32(typed.Max),
					},
				},
			})
		default:
			return nil, fmt.Errorf("waitForToProto: unknown SingleCondition type %T at index %d", singleCondition, index)
		}
	}
	return out, nil
}

func channelMessagesToProto(codec ObjectCodec, msgs []ChannelMessage) ([]*pb.ChannelPublish, error) {
	if len(msgs) == 0 {
		return nil, nil
	}
	out := make([]*pb.ChannelPublish, 0, len(msgs))
	for msgIndex, message := range msgs {
		values := make([]*pb.Value, 0, len(message.Values))
		for valueIndex, value := range message.Values {
			protoValue, err := codec.EncodeValue(value)
			if err != nil {
				return nil, fmt.Errorf("channelMessagesToProto: msg[%d].value[%d] for channel %q: %w", msgIndex, valueIndex, message.ChannelName, err)
			}
			values = append(values, protoValue)
		}
		out = append(out, &pb.ChannelPublish{
			ChannelName: message.ChannelName,
			Values:      values,
		})
	}
	return out, nil
}
