package main;

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	//_ "net/http/pprof"
	// "runtime"
	//"runtime/pprof"
	//"os"
	"sync"
    "time"

	"github.com/spf13/viper"
	httpclient "github.com/mreiferson/go-httpclient"
)

type Handler struct {
	BindTo 			string
	Mirror 			string
	SendTo 			string

	CertFile 		string
	KeyFile  		string

	MirrorHostName	string
	SendToHostName	string

	MirrorTimeout	time.Duration
	SendToTimeout	time.Duration

	Client			*http.Client
}

var (
	debug = flag.Bool("debug", false, "Be more verbose in logging")
	//cpuprofile = flag.String("cpuprofile", "", "Write cpu profile to file")
)

func (h Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var req1, req2 *http.Request

	// Mirroring traffic, if we should
	if len(h.Mirror) > 0 {
		req1, req2 = splitRequest(req)
		go func() {
			defer func() {
				if r := recover(); r != nil && *debug {
					fmt.Println("Recovered in Handler.ServeHTTP", r)
				}
			}()

			// Open new TCP connection to the server
			//connTCP, err := net.DialTimeout("tcp", h.Mirror, h.MirrorTimeout)
			//if err != nil {
			//	if *debug {
			//		fmt.Printf("Failed to connect to mirror: %v\n", err)
			//	}
			//	return
			//}

			//connHTTP := httputil.NewClientConn(connTCP, nil)
			//defer connHTTP.Close()

			if len(h.MirrorHostName) > 0 {
				req2.Host = h.MirrorHostName
			}

			// Write request to the wire
			err = connHTTP.Write(req2)
			if err != nil {
				if *debug {
					fmt.Printf("Failed to send to mirror (%s): %v\n", h.Mirror, err)
				}
				return
			}

			// Dump request, if we're asked to
			// if *debug {
			// 	dump, _ := httputil.DumpRequest(req2, true)
			// 	fmt.Println(string(dump))
			// }

			// Read response
			//resp, err := connHTTP.Read(req2)
			req2.URL.Scheme = "http"
			req2.URL.Host = h.Mirror
			resp, err := h.Client.Do(req2)
			if err != nil && err != httputil.ErrPersistEOF {
				if *debug {
					fmt.Printf("Failed to receive from mirror (%s): %v\n", h.Mirror, err)
				}
				return
			}

			_, _ = ioutil.ReadAll(resp.Body)

		}()
	} else {
		req1 = req
	}

	defer func() {
		if r := recover(); r != nil && *debug {
			fmt.Println("Recovered in Handler.ServeHTTP", r)
		}
	}()

	// Open new TCP connection to the server
	connTCP, err := net.DialTimeout("tcp", h.SendTo, h.SendToTimeout)
	if err != nil {
		if *debug {
			fmt.Printf("Failed to connect: %v\n", err)
		}
		w.WriteHeader(503)
		return
	}

	connHTTP := httputil.NewClientConn(connTCP, nil)
	defer connHTTP.Close()

	//req1 := cloneRequest(req)
	// req1.Close = true
	if len(h.SendToHostName) > 0 {
		req1.Host = h.SendToHostName
	}

	// Dump request, if we're asked to
	// if *debug {
	// 	dump, _ := httputil.DumpRequest(req1, true)
	// 	fmt.Println(string(dump))
	// }

	// Write request to the wire
	err = connHTTP.Write(req1)
	if err != nil {
		if *debug {
			fmt.Printf("Failed to send to %s: %v\n", h.SendTo, err)
		}
		w.WriteHeader(503)
		return
	}

	// Read response
	resp, err := connHTTP.Read(req1)
	// resp, err := connHTTP.Do(req1)
	if err != nil && err != httputil.ErrPersistEOF {
		if *debug {
			fmt.Printf("Failed to receive from %s: %v\n", h.SendTo, err)
		}
		w.WriteHeader(503)
		return
	}

	// Dump response, if we're asked to	
	// if *debug {
	// 	dump, err := httputil.DumpResponse(resp, true)
	// 	if err == nil {
	// 		fmt.Println(string(dump))
	// 	}
	// }

	// Copy response headers
	for k, v := range resp.Header {
		w.Header()[k] = v
	}

	// w.Header().Set("Connection", "Close")
	w.WriteHeader(resp.StatusCode)

	body, _ := ioutil.ReadAll(resp.Body)
	w.Write(body)
}

func splitRequest(src *http.Request) (dst1 *http.Request, dst2 *http.Request) {
	var b1,b2 *bytes.Buffer
	var w io.Writer
	defer src.Body.Close()
	b1 = new(bytes.Buffer)
	b2 = new(bytes.Buffer)
	w = io.MultiWriter(b1, b2)
	io.Copy(w, src.Body)
	dst1 = &http.Request {
		Method:			src.Method,
		URL:			src.URL,
		Proto:			src.Proto,
		ProtoMajor:		src.ProtoMajor,
		ProtoMinor:		src.ProtoMinor,
		Header:			src.Header,
		Body:			ioutil.NopCloser(b1),
		Host:			src.Host,
		ContentLength:	src.ContentLength,
	}
	dst2 = &http.Request {
		Method:			src.Method,
		URL:			src.URL,
		Proto:			src.Proto,
		ProtoMajor:		src.ProtoMajor,
		ProtoMinor:		src.ProtoMinor,
		Header:			src.Header,
		Body:			ioutil.NopCloser(b2),
		Host:			src.Host,
		ContentLength:	src.ContentLength,
	}
	return
}

func init() {
	flag.Parse()
	viper.SetConfigType("properties")
	viper.SetConfigName("fink")
	viper.AddConfigPath("/etc/fink") 
	viper.AddConfigPath(".")
	err := viper.ReadInConfig() // Find and read the config file
	if err != nil { // Handle errors reading the config file
    	panic(fmt.Errorf("Fatal error config file: %s \n", err))
	}
}

func main() {
	var config []Handler
	var i int = 0
	var wg sync.WaitGroup

	//if *cpuprofile != "" {
	//	f, err := os.Create(*cpuprofile)
	//	if err != nil {
	//		panic(fmt.Errorf("Failed to create file %s: %v\n", *cpuprofile, err))
	//	}
	//	pprof.StartCPUProfile(f)
	//	defer pprof.StopCPUProfile()
	//}

	//go func() {
	//	fmt.Println(http.ListenAndServe("[::]:6060", nil))
	//}()

	for {
		var item = Handler{}
		var key string

		// Where to listen
		key = fmt.Sprintf("listen.%d", i)
		if ! viper.IsSet(key) {
			break
		}
		item.BindTo = viper.GetString(key)

		// Primary destination to forward requests
		key = fmt.Sprintf("sendto.%d", i)
		if ! viper.IsSet(key) {
			break
		}
		item.SendTo = viper.GetString(key)

		// Destination to mirror traffic to (can be empty)
		key = fmt.Sprintf("mirror.%d", i)
		if viper.IsSet(key) {
			item.Mirror = viper.GetString(key)
		}

		// Certificate file
		key = fmt.Sprintf("cert.%d", i)
		if viper.IsSet(key) {
			item.CertFile = viper.GetString(key)
		}

		// Key file (should be without password)
		key = fmt.Sprintf("key.%d", i)
		if viper.IsSet(key) {
			item.KeyFile = viper.GetString(key)
		}

		// Hostname (if it should be set)
		key = fmt.Sprintf("sendto.hostname.%d", i)
		if viper.IsSet(key) {
			item.SendToHostName = viper.GetString(key)
		}

		// Hostname (if it should be set)
		key = fmt.Sprintf("mirror.hostname.%d", i)
		if viper.IsSet(key) {
			item.MirrorHostName = viper.GetString(key)
		}

		// Timeout for getting response from the main target, ms
		key = fmt.Sprintf("sendto.timeout.%d", i)
		if viper.IsSet(key) {
			item.SendToTimeout = time.Duration(time.Duration(viper.GetInt(key)) * time.Millisecond)
		} else {
			item.SendToTimeout = time.Millisecond * 1000
		}

		// Timeout for getting response from mirror target, ms
		key = fmt.Sprintf("mirror.timeout.%d", i)
		if viper.IsSet(key) {
			item.MirrorTimeout = time.Duration(time.Duration(viper.GetInt(key)) * time.Millisecond)
		} else {
			item.MirrorTimeout = time.Millisecond * 1000
		}

		config = append(config, item)
		i += 1
	}

	if len(config) < 1 {
		panic(fmt.Sprintf("No valid configuration found!"))
	}


	wg.Add(len(config))
	for i = range config {
		go func(h Handler) {
			defer wg.Done()
			var err error
			var listener net.Listener
			var transport *httpclient.Transport

			if len(h.CertFile) > 0 { // Bind to SSL-socket, if key/cert files are present
				cert, err := tls.LoadX509KeyPair(h.CertFile, h.KeyFile)
				if err != nil {
					panic(fmt.Errorf("Failed to load certficate: %s and private key: %s\n", h.CertFile, h.KeyFile))
					return
				}

				tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
				listener, err = tls.Listen("tcp", h.BindTo, tlsConfig)
				if err != nil {
					panic(fmt.Errorf("Failed to listen to %s: %s\n", h.BindTo, err))
					return
				}
			} else { // Otherwise simply listen to the socket
				listener, err = net.Listen("tcp", h.BindTo)
				if err != nil {
					panic(fmt.Errorf("Failed to listen to %s: %s\n", h.BindTo, err))
					return
				}
			}

			transport = &httpclient.Transport {
				ConnectTimeout:        50 * time.Millisecond,
				RequestTimeout:        h.SendToTimeout,
				//ResponseHeaderTimeout: 5*time.Second,
			}
			defer transport.Close()

			h.Client = &http.Client{Transport: transport}

			http.Serve(listener, h)
		} (config[i])
	}

	wg.Wait()
}