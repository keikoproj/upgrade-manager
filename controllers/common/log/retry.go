/*
Copyright 2021 Intuit Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
		DefaultRetryer: retryer,
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
	Infof("retryable: %v -- %v, will retry after %v", err, method, duration)

	return duration
}
