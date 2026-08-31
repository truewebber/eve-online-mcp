package mocks

import "go.uber.org/mock/gomock"

const logArgCeil = 8

func QuietLogger(ctrl *gomock.Controller) *MockLogger {
	l := NewMockLogger(ctrl)
	allowLogs(l)

	return l
}

func allowLogs(l *MockLogger) {
	for n := range logArgCeil + 1 {
		args := make([]any, n+1)
		for i := range args {
			args[i] = gomock.Any()
		}
		l.EXPECT().Info(args[0], args[1:]...).AnyTimes()
		l.EXPECT().Error(args[0], args[1:]...).AnyTimes()
	}
}
