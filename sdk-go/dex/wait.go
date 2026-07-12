package dex

import "time"

type waitType int

const (
	waitTypeAnyOf waitType = iota
	waitTypeAllOf
)

// WaitForCondition is the return type of Step.WaitFor.
type WaitForCondition interface {
	isWaitForCondition()
}

type waitForConditionImpl struct {
	waitType   waitType
	conditions []SingleCondition
}

func (waitForConditionImpl) isWaitForCondition() {}

func waitConditionType(condition WaitForCondition) waitType {
	return condition.(*waitForConditionImpl).waitType
}

func waitConditionList(condition WaitForCondition) []SingleCondition {
	return condition.(*waitForConditionImpl).conditions
}

// AnyOf returns a WaitForCondition satisfied when any single condition is met.
func AnyOf(conditions ...SingleCondition) WaitForCondition {
	return &waitForConditionImpl{
		waitType:   waitTypeAnyOf,
		conditions: conditions,
	}
}

// AllOf returns a WaitForCondition satisfied when all conditions are met.
func AllOf(conditions ...SingleCondition) WaitForCondition {
	return &waitForConditionImpl{
		waitType:   waitTypeAllOf,
		conditions: conditions,
	}
}

// SingleCondition is one atomic thing to wait for (a timer or a channel message).
type SingleCondition interface {
	singleCondition()
}

type timerCondition struct {
	Duration time.Duration
}

func (timerCondition) singleCondition() {}

// Timer creates a SingleCondition that fires after the given duration.
func Timer(duration time.Duration) SingleCondition {
	return timerCondition{Duration: duration}
}

type channelCondition struct {
	ChannelName string
	Min         int
	Max         int
}

func (channelCondition) singleCondition() {}
