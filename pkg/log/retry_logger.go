package log

import (
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/request"
)

type RetryLogger struct {
	client.DefaultRetryer
}

var _ request.Retryer = &RetryLogger{}

func NewRetryLogger(retryer client.DefaultRetryer) *RetryLogger {
	return &RetryLogger{
		retryer,
	}
}

func (l RetryLogger) RetryRules(r *request.Request) time.Duration {
	var (
		duration = l.DefaultRetryer.RetryRules(r)
		service  = r.ClientInfo.ServiceName
		name     string
		err      string
	)

	if r.Operation != nil {
		name = r.Operation.Name
	}
	method := fmt.Sprintf("%v/%v", service, name)

	if r.Error != nil {
		err = fmt.Sprintf("%v", r.Error)
	} else {
		err = fmt.Sprintf("%d %s", r.HTTPResponse.StatusCode, r.HTTPResponse.Status)
	}
	Debugf("retryable: %v -- %v, will retry after %v", err, method, duration)

	return duration
}
