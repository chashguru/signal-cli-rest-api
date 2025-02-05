package main

import (
	"encoding/json"
	"fmt"
	"github.com/bbernhard/signal-cli-rest-api/utils"
	"github.com/gabriel-vasile/mimetype"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const supervisorctlConfigTemplate = `
[program:%s]
environment=JAVA_HOME=/opt/java/openjdk
process_name=%s
command=bash -c "nc -l -p %d <%s | signal-cli --output=json -u %s --config %s jsonRpc >%s"
autostart=true
autorestart=true
startretries=10
user=signal-api
directory=/usr/bin/
redirect_stderr=true
stdout_logfile=/var/log/%s/out.log
stderr_logfile=/var/log/%s/err.log
stdout_logfile_maxbytes=50MB
stdout_logfile_backups=10
numprocs=1
`

func isSignalCliLinkedNumberConfigFile(filename string) (bool, error) {
	fileExtension := filepath.Ext(filename)
	if fileExtension != "" {
		return false, nil
	}

	mimetype, err := mimetype.DetectFile(filename)
	if err != nil {
		return false, err
	}
	if mimetype.String() == "application/json" {
		return true, nil
	}
	return false, nil
}

func getUsernameFromLinkedNumberConfigFile(filename string) (string, error) {
	type LinkedNumberConfigFile struct {
		Username string `json:"username"`
	}
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", err
	}
	var linkedNumberConfigFile LinkedNumberConfigFile
	err = json.Unmarshal(bytes, &linkedNumberConfigFile)
	if err != nil {
		return "", err
	}
	return linkedNumberConfigFile.Username, nil
}
func main() {
	signalCliConfigDir := "/home/.local/share/signal-cli/"
	signalCliConfigDirEnv := utils.GetEnv("SIGNAL_CLI_CONFIG_DIR", "")
	if signalCliConfigDirEnv != "" {
		signalCliConfigDir = signalCliConfigDirEnv
		if !strings.HasSuffix(signalCliConfigDirEnv, "/") {
			signalCliConfigDir += "/"
		}
	}

	signalCliConfigDataDir := signalCliConfigDir + "data"

	jsonRpc2ClientConfig := utils.NewJsonRpc2ClientConfig()

	var tcpBasePort int64 = 6000
	fifoBasePathName := "/tmp/sigsocket"
	var ctr int64 = 0

	items, err := ioutil.ReadDir(signalCliConfigDataDir)
	if err != nil {
		log.Fatal("Couldn't read contents of ", signalCliConfigDataDir, ". Is your phone number properly registered? Please be aware that registering a phone number only works in normal/native mode and is currently not supported in json-rpc mode!")
	}
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		filename := filepath.Base(item.Name())
		isSignalCliLinkedNumberConfigFile, err := isSignalCliLinkedNumberConfigFile(signalCliConfigDataDir + "/" + filename)
		if err != nil {
			log.Error("Couldn't determine whether file ", filename, " is a signal-cli config file: ", err.Error())
			continue
		}

		if strings.HasPrefix(filename, "+") || isSignalCliLinkedNumberConfigFile {
			var number string = ""
			if utils.IsPhoneNumber(filename) {
				number = filename
			} else if isSignalCliLinkedNumberConfigFile {
				number, err = getUsernameFromLinkedNumberConfigFile(signalCliConfigDataDir + "/" + filename)
				if err != nil {
					log.Debug("Skipping ", filename, " as it is not a valid signal-cli config file: ", err.Error())
					continue
				}
			} else {
				log.Error("Skipping ", filename, " as it is not a valid phone number!")
				continue
			}

			fifoPathname := fifoBasePathName + strconv.FormatInt(ctr, 10)
			tcpPort := tcpBasePort + ctr
			jsonRpc2ClientConfig.AddEntry(number, utils.JsonRpc2ClientConfigEntry{TcpPort: tcpPort, FifoPathname: fifoPathname})
			ctr += 1

			os.Remove(fifoPathname) //remove any existing named pipe

			_, err = exec.Command("mkfifo", fifoPathname).Output()
			if err != nil {
				log.Fatal("Couldn't create fifo with name ", fifoPathname, ": ", err.Error())
			}

			uid := utils.GetEnv("SIGNAL_CLI_UID", "1000")
			gid := utils.GetEnv("SIGNAL_CLI_GID", "1000")
			_, err = exec.Command("chown", uid+":"+gid, fifoPathname).Output()
			if err != nil {
				log.Fatal("Couldn't change permissions of fifo with name ", fifoPathname, ": ", err.Error())
			}

			supervisorctlProgramName := "signal-cli-json-rpc-" + strconv.FormatInt(ctr, 10)
			supervisorctlLogFolder := "/var/log/" + supervisorctlProgramName
			_, err = exec.Command("mkdir", "-p", supervisorctlLogFolder).Output()
			if err != nil {
				log.Fatal("Couldn't create log folder ", supervisorctlLogFolder, ": ", err.Error())
			}

			log.Info("Found number ", number, " and added it to jsonrpc2.yml")

			//write supervisorctl config
			supervisorctlConfigFilename := "/etc/supervisor/conf.d/" + "signal-cli-json-rpc-" + strconv.FormatInt(ctr, 10) + ".conf"
			supervisorctlConfig := fmt.Sprintf(supervisorctlConfigTemplate, supervisorctlProgramName, supervisorctlProgramName,
				tcpPort, fifoPathname, number, signalCliConfigDir, fifoPathname, supervisorctlProgramName, supervisorctlProgramName)
			err = ioutil.WriteFile(supervisorctlConfigFilename, []byte(supervisorctlConfig), 0644)
			if err != nil {
				log.Fatal("Couldn't write ", supervisorctlConfigFilename, ": ", err.Error())
			}
		}
	}

	// write jsonrpc.yml config file
	err = jsonRpc2ClientConfig.Persist(signalCliConfigDir + "jsonrpc2.yml")
	if err != nil {
		log.Fatal("Couldn't persist jsonrpc2.yaml: ", err.Error())
	}
}
