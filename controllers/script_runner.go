/*
Copyright 2019 Intuit, Inc..

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

package controllers

import (
	"fmt"
	"github.com/go-logr/logr"
	upgrademgrv1alpha1 "github.com/keikoproj/upgrade-manager/api/v1alpha1"
	"github.com/pkg/errors"
	"os/exec"
	"strings"
)

const (
	// KubeCtlBinary is the path to the kubectl executable
	KubeCtlBinary = "/usr/local/bin/kubectl"
	// ShellBinary is the path to the shell executable
	ShellBinary = "/bin/sh"
)

type ScriptRunner struct {
	Log         logr.Logger
	KubectlCall string
}

func NewScriptRunner(logger logr.Logger) ScriptRunner {
	return ScriptRunner{
		Log:         logger,
		KubectlCall: KubeCtlBinary,
	}
}

func (r *ScriptRunner) uncordonNode(nodeName string, ruObj *upgrademgrv1alpha1.RollingUpgrade) (string, error) {
	script := fmt.Sprintf("%s uncordon %s", r.KubectlCall, nodeName)
	return r.runScript(script, false, ruObj)
}

func (r *ScriptRunner) drainNode(nodeName string, ruObj *upgrademgrv1alpha1.RollingUpgrade) (string, error) {
	// kops behavior implements the same behavior by using these flags when draining nodes
	// https://github.com/kubernetes/kops/blob/7a629c77431dda02d02aadf00beb0bed87518cbf/pkg/instancegroups/instancegroups.go lines 337-340
	script := fmt.Sprintf("%s drain %s --ignore-daemonsets=true --delete-local-data=true --force --grace-period=-1", r.KubectlCall, nodeName)
	return r.runScript(script, false, ruObj)
}

func (r *ScriptRunner) runScriptWithEnv(script string, background bool, ruObj *upgrademgrv1alpha1.RollingUpgrade, env []string) (string, error) {
	r.info(ruObj, "Running script", "script", script)
	command := exec.Command(ShellBinary, "-c", script)
	if env != nil {
		command.Env = env
	}

	if background {
		r.info(ruObj, "Running script in background. Logs not available.")
		err := command.Run()
		if err != nil {
			r.info(ruObj, fmt.Sprintf("Script finished with error: %s", err))
		}

		return "", nil
	}

	out, err := command.CombinedOutput()
	if err != nil {
		r.error(ruObj, err, "Script finished", "output", string(out))
	} else {
		r.info(ruObj, "Script finished", "output", string(out))
	}
	return string(out), err
}

func (r *ScriptRunner) runScript(script string, background bool, ruObj *upgrademgrv1alpha1.RollingUpgrade) (string, error) {
	return r.runScriptWithEnv(script, background, ruObj, nil)

}

// logger creates logger for rolling upgrade.
func (r *ScriptRunner) logger(ruObj *upgrademgrv1alpha1.RollingUpgrade) logr.Logger {
	return r.Log.WithValues("rollingupgrade", ruObj.Name)
}

// info logs message with Info level for the specified rolling upgrade.
func (r *ScriptRunner) info(ruObj *upgrademgrv1alpha1.RollingUpgrade, msg string, keysAndValues ...interface{}) {
	r.logger(ruObj).Info(msg, keysAndValues...)
}

// error logs message with Error level for the specified rolling upgrade.
func (r *ScriptRunner) error(ruObj *upgrademgrv1alpha1.RollingUpgrade, err error, msg string, keysAndValues ...interface{}) {
	r.logger(ruObj).Error(err, msg, keysAndValues...)
}

func (r *ScriptRunner) PostTerminate(instanceID string, nodeName string, ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	if ruObj.Spec.PostTerminate.Script != "" {

		out, err := r.runScriptWithEnv(ruObj.Spec.PostTerminate.Script, false, ruObj, r.buildEnv(ruObj, instanceID, nodeName))
		if err != nil {
			if strings.HasPrefix(out, "Error from server (NotFound)") {
				r.error(ruObj, err, "Node not found when running postTerminate. Ignoring ...", "output", out, "instanceID", instanceID)
				return nil
			}
			msg := "Failed to run postTerminate script"
			r.error(ruObj, err, msg, "instanceID", instanceID)
			return errors.Wrap(err, msg)
		}
	}
	return nil

}

func (r *ScriptRunner) PreDrain(instanceID string, nodeName string, ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	if ruObj.Spec.PreDrain.Script != "" {
		script := ruObj.Spec.PreDrain.Script
		_, err := r.runScriptWithEnv(script, false, ruObj, r.buildEnv(ruObj, instanceID, nodeName))
		if err != nil {
			msg := "Failed to run preDrain script"
			r.error(ruObj, err, msg)
			return errors.Wrap(err, msg)
		}
	}
	return nil

}
func (r *ScriptRunner) PostWait(instanceID string, nodeName string, ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	if ruObj.Spec.PostDrain.PostWaitScript != "" {
		_, err := r.runScriptWithEnv(ruObj.Spec.PostDrain.PostWaitScript, false, ruObj, r.buildEnv(ruObj, instanceID, nodeName))
		if err != nil {
			msg := "Failed to run postDrainWait script: " + err.Error()
			r.error(ruObj, err, msg)
			result := errors.Wrap(err, msg)

			r.info(ruObj, "Uncordoning the node %s since it failed to run postDrain Script", "nodeName", nodeName)
			_, err = r.uncordonNode(nodeName, ruObj)
			if err != nil {
				r.error(ruObj, err, "Failed to uncordon", "nodeName", nodeName)
			}
			return result
		}
	}
	return nil
}

func (r *ScriptRunner) PostDrain(instanceID string, nodeName string, ruObj *upgrademgrv1alpha1.RollingUpgrade) error {
	if ruObj.Spec.PostDrain.Script != "" {
		_, err := r.runScriptWithEnv(ruObj.Spec.PostDrain.Script, false, ruObj, r.buildEnv(ruObj, instanceID, nodeName))
		if err != nil {
			msg := "Failed to run postDrain script: "
			r.error(ruObj, err, msg)
			result := errors.Wrap(err, msg)

			r.info(ruObj, "Uncordoning the node %s since it failed to run postDrain Script", "nodeName", nodeName)
			_, err = r.uncordonNode(nodeName, ruObj)
			if err != nil {
				r.error(ruObj, err, "Failed to uncordon", "nodeName", nodeName)
			}
			return result
		}
	}
	return nil
}

func (r *ScriptRunner) buildEnv(ruObj *upgrademgrv1alpha1.RollingUpgrade, instanceID string, nodeName string) []string {
	return []string{
		fmt.Sprintf("%s=%s", asgNameKey, ruObj.Spec.AsgName),
		fmt.Sprintf("%s=%s", instanceIDKey, instanceID),
		fmt.Sprintf("%s=%s", instanceNameKey, nodeName),
	}
}
