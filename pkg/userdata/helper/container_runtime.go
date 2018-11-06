package helper

import (
	"bytes"
	"fmt"
	"text/template"
)

// DockerDaemonConfig returns the docker daemon.json with preferred settings
func DockerDaemonConfig() string {
	return `{
  "storage-driver": "overlay2",
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "2"
  }
}`
}

const dockerSystemdUnitTpl = `[Unit]
Description=Docker Application Container Engine
Documentation=https://docs.docker.com
After=network-online.target docker.socket firewalld.service
Wants=network-online.target
Requires=docker.socket

[Service]
Environment="PATH=/opt/bin:/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin/"
Type=notify
# the default is not to use systemd for cgroups because the delegate issues still
# exists and systemd currently does not support the cgroup feature set required
# for containers run by docker
ExecStart=/opt/bin/dockerd -H fd://
ExecReload=/bin/kill -s HUP $MAINPID
LimitNOFILE=1048576
# Having non-zero Limit*s causes performance problems due to accounting overhead
# in the kernel. We recommend using cgroups to do container-local accounting.
LimitNPROC=infinity
LimitCORE=infinity
# Uncomment TasksMax if your systemd version supports it.
# Only systemd 226 and above support this version.
{{ if .SetTasksMax }}
TasksMax=infinity
{{ end }}
TimeoutStartSec=0
# set delegate yes so that systemd does not reset the cgroups of docker containers
Delegate=yes
# kill only the docker process, not all processes in the cgroup
KillMode=process
# restart the docker process if it exits prematurely
Restart=on-failure
StartLimitBurst=3
StartLimitInterval=60s

[Install]
WantedBy=multi-user.target`

// DockerSystemdUnit returns the systemd unit for docker. setTasksMax should be set if the consumer uses systemd > 226 (Ubuntu & CoreoS - NOT CentOS)
func DockerSystemdUnit(setTasksMax bool) (string, error) {
	tmpl, err := template.New("docker-systemd-unit").Funcs(TxtFuncMap()).Parse(dockerSystemdUnitTpl)
	if err != nil {
		return "", fmt.Errorf("failed to parse docker-systemd-unit template: %v", err)
	}

	data := struct {
		SetTasksMax bool
	}{
		SetTasksMax: setTasksMax,
	}
	b := &bytes.Buffer{}
	err = tmpl.Execute(b, data)
	if err != nil {
		return "", fmt.Errorf("failed to execute docker-systemd-unit template: %v", err)
	}

	return string(b.String()), nil
}

// DockerSystemdUnit returns the systemd unit for docker
func DockerSystemdSocket() string {
	return `[Unit]
Description=Docker Socket for the API
PartOf=docker.service

[Socket]
ListenStream=/var/run/docker.sock
SocketMode=0660
SocketUser=root
SocketGroup=docker

[Install]
WantedBy=sockets.target`
}
