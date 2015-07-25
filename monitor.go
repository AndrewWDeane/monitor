package main

import (
	"code.google.com/p/go.net/websocket"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// types
type Connection struct {
	LocalHost          string
	LocalSocket        string
	LocalPid           string
	LocalInstanceName  string
	LocalInstanceType  string
	LocalStdout        string
	RemoteHost         string
	RemoteSocket       string
	RemotePid          string
	RemoteInstanceName string
	RemoteInstanceType string
	RemoteStdout       string
}

type Process struct {
	Host   string
	Name   string
	Status string
	Stdout string
	Type   string
}

type Link struct {
	From Process
	To   Process
}

type Instruction struct {
	Action         string
	Host           string
	Log            string
	ScreenName     string
	AdditionalJSON json.RawMessage
}

type Xref struct {
	From string
	To   string
}

var WebsocketHandler = websocket.Handler(WebsocketServer)
var knownHostsFileFlag = flag.String("knownHostsFile", "", "File containing the xref for known IPs and names")
var knownConnectionsFileFlag = flag.String("knownConnectionsFile", "", "File containing the xref for known IPs and ports")
var showSshFlag = flag.Bool("showSsh", false, "Send ssh nodes")
var serveLocalhostFlag = flag.Bool("serveLocalhost", false, "Serve as localhost")
var hostsCSVFlag = flag.String("hostsCSV", "", "CSV list of hosts")
var portFlag = flag.Int("port", 8081, "Port")
var pauseFlag = flag.Int("pause", 10, "Seconds to pause")
var defaultScreenFlag = flag.String("defaultScreen", "", "Default screen to send to clients")
var knownHostsFile string
var knownConnectionsFile string
var serveLocalhost bool
var showSsh bool
var hostsCSV string
var defaultScreen string
var port int
var pause int
var wsChannel = make(chan *websocket.Conn, 1000)
var mainChannel = make(chan string, 1000)
var myLog *log.Logger = log.New(os.Stdout, "", log.Ldate+log.Lmicroseconds)
var knownHosts = map[string]string{}
var knownConnections = map[string]string{}

var VERSION = "1.4.0"

func main() {

	myLog.Print(fmt.Sprintf("Starting %v", VERSION))

	flag.Parse()
	serveLocalhost = *serveLocalhostFlag
	hostsCSV = *hostsCSVFlag
	defaultScreen = *defaultScreenFlag
	port = *portFlag
	pause = *pauseFlag
	showSsh = *showSshFlag
	knownHostsFile = *knownHostsFileFlag
	knownConnectionsFile = *knownConnectionsFileFlag

	if hostsCSV == "" {
		hostsCSV = "localhost"
	}

	if knownHostsFile > "" {

		f, err := os.Open(knownHostsFile)
		if err != nil {
			myLog.Print(fmt.Sprintf("File error:%v", err))
		} else {
			myLog.Print("Loading known hosts file")
			dec := json.NewDecoder(f)
			for {
				var xref Xref
				if err := dec.Decode(&xref); err != nil {
					fmt.Println(fmt.Sprintf("decode error:%v", err))
					break
				}
				myLog.Print(fmt.Sprintf("%v", xref))
				knownHosts[xref.From] = xref.To
			}
		}

	}

	if knownConnectionsFile > "" {

		f, err := os.Open(knownConnectionsFile)
		if err != nil {
			myLog.Print(fmt.Sprintf("File error:%v", err))
		} else {
			myLog.Print("Loading known conenctions file")
			dec := json.NewDecoder(f)
			for {
				var xref Xref
				if err := dec.Decode(&xref); err != nil {
					fmt.Println(fmt.Sprintf("decode error:%v", err))
					break
				}
				myLog.Print(fmt.Sprintf("%v", xref))
				knownConnections[xref.From] = xref.To
			}
		}

	}

	// register the files with the filehandler
	http.HandleFunc("/palantir.html", FileHandler)
	http.HandleFunc("/palantirFuture.html", FileHandler)
	http.HandleFunc("/processing.js", FileHandler)
	http.HandleFunc("/jquery.min.js", FileHandler)
	http.HandleFunc("/json2.js", FileHandler)
	http.HandleFunc("/jquery-ui-1.8.17.custom.min.js", FileHandler)
	http.HandleFunc("/css/ui-lightness/jquery-ui-1.8.17.custom.css", FileHandler)
	http.HandleFunc("/css/vader/jquery-ui-1.8.17.custom.css", FileHandler)
	http.HandleFunc("/css/vader/", FileHandler)
	http.HandleFunc("/css/vader/images/", FileHandler)

	// register the websocket url with the procedure
	http.Handle("/websocket", WebsocketHandler)

	// create the array of connections
	var wsConnections = make([]*websocket.Conn, 0)

	// start main monitoring process
	go monitorHosts()
	// and the readers
	go func() {
		for {
			select {
			case newWs := <-wsChannel:
				// store in an array
				wsConnections = append(wsConnections, newWs)
				myLog.Print(fmt.Sprintf("Connection:%v %v", newWs.LocalAddr().String(), newWs.RemoteAddr().String()))
			case msg := <-mainChannel:
				// send msg on ws
				myLog.Print(fmt.Sprintf("Status:%v", msg))
				for i, ws := range wsConnections {
					if ws != nil {
						if _, err := ws.Write([]byte(msg)); err != nil {
							myLog.Print(fmt.Sprintf("Error:%v", err))
							wsConnections[i] = nil
						}
					}
				}
				// TODO copy all non-nil entries back to wsConnections
			}
		}
	}()

	//if err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%v", port), nil); err != nil {
	hostname, _ := os.Hostname()
	host, _ := net.LookupHost(hostname)
	if serveLocalhost {
		host = append(host, "127.0.0.1")
	}
	myLog.Print(fmt.Sprintf("Serving on %v:%v", host, port))
	if err := http.ListenAndServe(fmt.Sprintf("%v:%v", host, port), nil); err != nil {
		panic(err)
	}

}

func WebsocketServer(ws *websocket.Conn) {

	// set the reader off
	go func() {
		inBytes := make([]byte, 4096)
		message := ""
		for {
			if n, err := ws.Read(inBytes); err != nil {
				myLog.Print(fmt.Sprintf("Error reading:%v", err))
				break
			} else {
				thisMessage := string(inBytes[0:n])
				message += thisMessage
				myLog.Print(fmt.Sprintf("Read %v %v", ws.RemoteAddr().String(), thisMessage))

				//TODO use a sensible delimiter - scared to use \n or \t because of JSON formatting
				// this is rocksolid as it expects the delimiter to be at the end of the read
				// do it properly, store all bytes read and then process each 'message' in the range using the delimiter
				if strings.HasSuffix(message, "ENDOFMESSAGE") {

					message = message[0 : len(message)-12]

					// parse JSON
					var instruction Instruction
					err := json.Unmarshal([]byte(message), &instruction)
					if err == nil {
						switch instruction.Action {
						// tail log
						case "Tail":
							cmd := exec.Command("ssh", instruction.Host, fmt.Sprintf("tail %v", instruction.Log))
							output, _ := cmd.CombinedOutput()
							outputLines := strings.Split(string(output), "\n")
							for _, line := range outputLines {
								message = fmt.Sprintf("println:%v", line)
								ws.Write([]byte(message))
							}
						// Save the screen
						case "SaveScreen":
							if instruction.ScreenName != "" && instruction.AdditionalJSON != nil {
								screenBytes := []byte(strings.Replace(string(instruction.AdditionalJSON), "\\\"", "\"", -1))
								// change the screen name to make it safer
								instruction.ScreenName = "./dev/monitorScreen_" + strings.Replace(instruction.ScreenName, "/", "_", -1)
								err := ioutil.WriteFile(instruction.ScreenName, screenBytes[1:len(screenBytes)-1], os.ModePerm)
								if err != nil {
									myLog.Print(fmt.Sprintf("Error writing file:%v", err))
								} else {
									myLog.Print(fmt.Sprintf("Saved %v", instruction.ScreenName))
								}

							}
						case "LoadScreen":
							// find file and send with reload:on the front
							// change the screen name to make it safer
							instruction.ScreenName = "./dev/monitorScreen_" + strings.Replace(instruction.ScreenName, "/", "_", -1)
							screen, err := ioutil.ReadFile(instruction.ScreenName)
							if err == nil && instruction.ScreenName != "" {
								screen = append([]byte("reload:"), screen...)
								myLog.Print(fmt.Sprintf("%v", string(screen)))
								ws.Write(screen)
							}

						} // Action switch

					} else {
						myLog.Print(fmt.Sprintf("JSON error:%v", err))
					}

					message = ""

				} // complete message

			} // socket read

		} // socket read loop
	}()

	// register with the main
	wsChannel <- ws

	if defaultScreen > "" {
		// even though it is json we're just going to send the text to the websocket
		// read whole file into var and send.
		message, err := ioutil.ReadFile(defaultScreen)
		if err == nil {
			ws.Write(message)
		}
	}

	for {
		// just keep the server alive
		time.Sleep(10e9)
	}

}

// flat file handler
func FileHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, fmt.Sprintf("./%v", r.URL.Path[1:]))
}

func lsofHost(host string, ch chan string) {

	//cmd := exec.Command("ssh", host, "lsof -i TCP -n -P")
	cmd := exec.Command("ssh", host, "lsof -n -P")
	output, _ := cmd.CombinedOutput()
	outputLines := strings.Split(string(output), "\n")
	stdout := ""
	pid := ""

	dbConnection := map[string]string{}

	for _, line := range outputLines {
		if line != "" {
			elements := strings.Fields(line)
			if pid != elements[1] {
				stdout = ""
				dbConnection = map[string]string{}
			}
			pid = elements[1]
			localEnd := ""
			remoteEnd := ""
			if len(elements) >= 9 {

				if elements[3] == "1u" || elements[3] == "1w" || elements[3] == "1wr" {
					stdout = elements[8]
				}

				if strings.HasSuffix(elements[8], ".db") {
					_, found := dbConnection[elements[8]]
					if !found {
						// manufacture a direct database connection
						localEnd = fmt.Sprintf("%v:%v_%v", host, pid, elements[8])
						dbName := strings.Split(elements[8], "/")
						dbName = strings.Split(dbName[len(dbName)-1], ".db")
						remoteEnd = fmt.Sprintf("%v:%v", host, elements[8])
						ch <- fmt.Sprintf("connection,%v,%v,%v,%v,%v", host, pid, localEnd, remoteEnd, stdout)
						ch <- fmt.Sprintf("connection,%v,%v,%v,%v,%v", host, fmt.Sprintf("%v:%v", host, dbName[0]), remoteEnd, localEnd, "")

						// Only one connection per db per pid
						dbConnection[elements[8]] = "found"
					}
				}
			}

			// socket connections
			if len(elements) >= 10 && elements[7] == "TCP" && elements[9] == "(ESTABLISHED)" {
				connections := strings.Split(elements[8], "->")
				localEnd = strings.Replace(strings.Replace(connections[0], "127.0.0.1", host, -1), "localhost", host, -1)
				remoteEnd = strings.Replace(strings.Replace(connections[1], "127.0.0.1", host, -1), "localhost", host, -1)
				ch <- fmt.Sprintf("connection,%v,%v,%v,%v,%v", host, pid, localEnd, remoteEnd, stdout)
			} else if len(elements) >= 9 && elements[6] == "TCP" && elements[8] == "(ESTABLISHED)" {
				connections := strings.Split(elements[7], "->")
				localEnd = strings.Replace(strings.Replace(connections[0], "127.0.0.1", host, -1), "localhost", host, -1)
				remoteEnd = strings.Replace(strings.Replace(connections[1], "127.0.0.1", host, -1), "localhost", host, -1)
				ch <- fmt.Sprintf("connection,%v,%v,%v,%v,%v", host, pid, localEnd, remoteEnd, stdout)
			} else if len(elements) >= 9 && elements[7] == "UDP" {
				//TODO these exclude UDP groups need to be args
				if !strings.HasPrefix(elements[8], "*") && !strings.HasPrefix(elements[8], "239.1.1.") {
					localEnd = fmt.Sprintf("%v:pid%v", host, pid)
					remoteEnd = elements[8] + "u"
					ch <- fmt.Sprintf("connection,%v,%v,%v,%v,%v", host, pid, localEnd, remoteEnd, stdout)
				}
			}

		}
	}
	ch <- "end"
}

func psHost(host string, ch chan string) {

	cmd := exec.Command("ssh", host, "ps -elfww")
	output, _ := cmd.CombinedOutput()
	outputLines := strings.SplitN(string(output), "\n", -1)

	for _, line := range outputLines {
		if line != "" {
			elements := strings.Fields(line)
			pid := ""
			var cmdLine []string
			if len(elements) >= 4 {
				pid = elements[3]
				if len(elements) >= 15 {
					cmdLine = elements[14:]

					executableArray := strings.Split(cmdLine[0], "/")
					executable := ""
					if len(executableArray) > 0 {
						executable = executableArray[len(executableArray)-1]
					}

					instanceName := ""
					processType := ""

					//TODO notes.
					// can we turn this into a JSON backed map, which finds the executable and then looks what to do?
					// different logic is needed for each though as some are elements on others are blah blah.

					switch executable {
					case "_progres":
						// progress instances, find -U in the arg array, instance name is the next entry
						for i, e := range cmdLine {
							if e == "-U" {
								if (i + 1) < len(cmdLine) {
									instanceName = cmdLine[i+1]
									processType = "progress"
								}
								break
							}
						}
					case "java":
						// lots of variants here
						for i, e := range cmdLine {
							if e == "-classpath" {
								if (i + 1) < len(cmdLine) {
									classpath := cmdLine[i+1]
									// if it contains sonic_Client jar then its a sonicClient
									if strings.Contains(classpath, "sonic_Client") {
										//instanceName = "sonicClient_" + pid // temp pid on end, again need a better way of doing this
										instanceName = "sonicClient_" + host
										processType = "sonicClient"
										break
									}
								}
							} else if strings.HasPrefix(e, "com.myorg.") {
								if (i + 3) < len(cmdLine) {
									instanceName = cmdLine[i+2] + "_" + cmdLine[i+3]
									processType = "java"
									break
								}
							}

						}
					case "ssh":
						// if the cmdLine contains either ps or lsof it is more than likely us so ignore it
						instanceName = strings.Join(cmdLine, "_")
						processType = "ssh"
						if len(cmdLine) >= 2 && (cmdLine[1] == "ps" || cmdLine[1] == "lsof") {
							processType = "ssh_mon"
						}
					case "sshd", "sshd:":
						// ignore notty root ssh clients - its probably us
						instanceName = strings.Join(cmdLine, "_")
						processType = "ssh"
						if len(cmdLine) >= 2 && cmdLine[1] == "root@notty" {
							processType = "ssh_mon"
						}

					// for testing
					//case "chrome", "ssh", "netcat":
					case "netcat":
						//instanceName = executable + pid
						instanceName = strings.Join(cmdLine, "_")
						processType = "netcat"
					default:
						// do something?
						processType = executable
						instanceName = ""
						instanceName = strings.Join(cmdLine, "_")
					}

					if instanceName != "" {
						ch <- fmt.Sprintf("instance,%v,%v,%v,%v", instanceName, host, pid, processType)
					}
				}
			}
		}
	}
	ch <- "end"

}

func monitorHosts() {

	var monitorChannel = make(chan string, 1000)
	var processes = map[string]string{}
	var statuses = map[string]string{}
	var connections = map[string]Connection{}
	var prevConnections = map[string]Connection{}
	var newConnections = map[string]Connection{}

	procs := 0
	hosts := strings.Split(hostsCSV, ",")

	for {

		for _, host := range hosts {
			myLog.Print(fmt.Sprintf("Processing:%v", host))
			go psHost(host, monitorChannel)
			go lsofHost(host, monitorChannel)
			procs += 2
		}

		for {
			message, ok := <-monitorChannel

			if !ok {
				myLog.Print("Monitor channel closed")
				break
			}

			if message == "end" {
				procs -= 1
			} else {
				message = strings.Replace(message, "localhost", "127.0.0.1", -1)
				elements := strings.Split(message, ",")
				if elements[0] == "instance" {

					//store instance against the host:pid
					processes[fmt.Sprintf("%v;%v", elements[2], elements[3])] = fmt.Sprintf("%v,%v", elements[1], elements[4])
				} else if elements[0] == "connection" {
					// store the connections - connection,host,pid,localEnd,remoteEnd
					localEnd := strings.Split(elements[3], ":")
					remoteEnd := strings.Split(elements[4], ":")

					// try to find these first and update if available
					localKey := fmt.Sprintf("%v;%v", localEnd[0], localEnd[1])
					remoteKey := fmt.Sprintf("%v;%v", remoteEnd[0], remoteEnd[1])

					connection, found := connections[localKey]
					if !found {
						connection = Connection{LocalHost: localEnd[0], LocalSocket: localEnd[1], LocalPid: elements[2], LocalStdout: elements[5], RemoteHost: remoteEnd[0], RemoteSocket: remoteEnd[1]}
					} else {
						// we found it so it was added as a remote connection, so update the pid
						if connection.RemotePid == "" {
							connection.RemotePid = elements[2]
						}
						if connection.RemoteStdout == "" {
							connection.RemoteStdout = elements[5]
						}
					}
					connections[localKey] = connection
					connections[remoteKey] = connection
				}
			}

			if procs == 0 {
				break
			}
		}

		mainChannel <- "status begin"

		// populate the instance names from the connections
		for _, connection := range connections {
			instanceName, found := processes[fmt.Sprintf("%v;%v", connection.LocalHost, connection.LocalPid)]

			if found {
				instanceNameArray := strings.Split(instanceName, ",")
				connection.LocalInstanceName = instanceNameArray[0]
				connection.LocalInstanceType = instanceNameArray[1]
			} else {
				connectionName := fmt.Sprintf("%v:%v", connection.LocalHost, connection.LocalSocket)
				if xrefHost, ok := knownConnections[connectionName]; ok {
					connectionName = xrefHost
				}

				connection.LocalInstanceName = connectionName

				if xrefHost, ok := knownHosts[connection.LocalHost]; ok {
					connection.LocalHost = xrefHost
				}

			}

			instanceName, found = processes[fmt.Sprintf("%v;%v", connection.RemoteHost, connection.RemotePid)]
			if found {
				instanceNameArray := strings.Split(instanceName, ",")
				connection.RemoteInstanceName = instanceNameArray[0]
				connection.RemoteInstanceType = instanceNameArray[1]
			} else {
				//look in the map to xref the host, that way we can name known clients
				connectionName := fmt.Sprintf("%v:%v", connection.RemoteHost, connection.RemoteSocket)
				if xrefHost, ok := knownConnections[connectionName]; ok {
					connectionName = xrefHost
				} else {
					// for remote hosts see if we can name the host
					if xrefHost, ok := knownHosts[connection.RemoteHost]; ok {
						connectionName = fmt.Sprintf("%v:%v", xrefHost, connection.RemoteSocket)
						// foul the host name, so that we dont rename the host field too
						connection.RemoteHost = connection.RemoteHost + "."
					}
				}
				connection.RemoteInstanceName = connectionName
			}

			localStatus := "ok"
			remoteStatus := "ok"

			key := fmt.Sprintf("%v:%v;%v:%v", connection.LocalHost, connection.LocalInstanceName, connection.RemoteHost, connection.RemoteInstanceName)
			revKey := fmt.Sprintf("%v:%v;%v:%v", connection.RemoteHost, connection.RemoteInstanceName, connection.LocalHost, connection.LocalInstanceName)
			prev, found := prevConnections[key]

			if found {

				//TODO notes.
				// here we could, dependant on the type of process, look inside the log file for patterns, or age (check size since last iteration)
				// what to do can be supplied by the JOSN backed config file, loaded into a map that then gives the variables (time patterns etc)

				// check that the pids and sockets are the same, if not then warning
				if connection.LocalPid != prev.LocalPid || connection.LocalSocket != prev.LocalSocket {
					localStatus = "warning"
				}
				if connection.RemotePid != prev.RemotePid || connection.RemoteSocket != prev.RemoteSocket {
					remoteStatus = "warning"
				}

				// check that its not the same connection but we found it from the other way
				if connection.LocalPid == prev.RemotePid && connection.LocalSocket == prev.RemoteSocket {
					localStatus = "ok"
					remoteStatus = "ok"
				}
			}

			_, found = newConnections[key]
			if !found {
				from := Process{Host: connection.LocalHost, Name: connection.LocalInstanceName, Status: localStatus, Stdout: connection.LocalStdout, Type: connection.LocalInstanceType}
				to := Process{Host: connection.RemoteHost, Name: connection.RemoteInstanceName, Status: remoteStatus, Stdout: connection.RemoteStdout, Type: connection.RemoteInstanceType}
				link := Link{From: from, To: to}

				if from.Type != "ssh_mon" && to.Type != "ssh_mon" {
					// xref any known hosts for display
					if xrefHost, ok := knownHosts[link.From.Host]; ok {
						link.From.Host = xrefHost
					}
					if xrefHost, ok := knownHosts[link.To.Host]; ok {
						link.To.Host = xrefHost
					}
					jsonData, _ := json.Marshal(link)
					mainChannel <- fmt.Sprintf("%v", string(jsonData))

					prevConnections[key] = connection
					prevConnections[revKey] = connection
					newConnections[key] = connection
					newConnections[revKey] = connection

					localKey := fmt.Sprintf("%v:%v", connection.LocalHost, connection.LocalInstanceName)
					remoteKey := fmt.Sprintf("%v:%v", connection.RemoteHost, connection.RemoteInstanceName)
					statuses[localKey] = localStatus
					statuses[remoteKey] = remoteStatus
				}
			}

		}

		// now for the failed / missing connections
		for _, prev := range prevConnections {

			key := fmt.Sprintf("%v:%v;%v:%v", prev.LocalHost, prev.LocalInstanceName, prev.RemoteHost, prev.RemoteInstanceName)
			revKey := fmt.Sprintf("%v:%v;%v:%v", prev.RemoteHost, prev.RemoteInstanceName, prev.LocalHost, prev.LocalInstanceName)
			_, found := newConnections[key]

			if !found {
				// find out which end of the link has failed and only set its status to error
				localKey := fmt.Sprintf("%v:%v", prev.LocalHost, prev.LocalInstanceName)
				localStatus, found := statuses[localKey]
				if !found {
					localStatus = "error"
					statuses[localKey] = "error"
				}
				remoteKey := fmt.Sprintf("%v:%v", prev.RemoteHost, prev.RemoteInstanceName)
				remoteStatus, found := statuses[remoteKey]
				if !found {
					remoteStatus = "error"
					statuses[remoteKey] = "error"
				}
				from := Process{Host: prev.LocalHost, Name: prev.LocalInstanceName, Status: localStatus}
				to := Process{Host: prev.RemoteHost, Name: prev.RemoteInstanceName, Status: remoteStatus}
				link := Link{From: from, To: to}

				// xref any known hosts for display
				if xrefHost, ok := knownHosts[link.From.Host]; ok {
					link.From.Host = xrefHost
				}
				if xrefHost, ok := knownHosts[link.To.Host]; ok {
					link.To.Host = xrefHost
				}
				jsonData, _ := json.Marshal(link)
				mainChannel <- fmt.Sprintf("%v", string(jsonData))

				// delete the previous connection
				// and its revkey?
				delete(prevConnections, key)
				delete(prevConnections, revKey)

			}

		}

		mainChannel <- "status end"

		connections = map[string]Connection{}
		processes = map[string]string{}
		newConnections = map[string]Connection{}
		statuses = map[string]string{}

		myLog.Print("Sleeping")
		time.Sleep(time.Duration(pause) * time.Second)
	}
}
