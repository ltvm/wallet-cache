package node

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

var cacheMethods = []string{}

type JSONRPCMessage struct {
	Version string   `json:"jsonrpc,omitempty"`
	ID      int      `json:"id,omitempty"`
	Method  string   `json:"method,omitempty"`
	Params  []string `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	Version string      `json:"jsonrpc,omitempty"`
	ID      int         `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
}

type NodeCache struct {
	client        *http.Client
	cacheResponse map[string]JSONRPCResponse // cache map with key is method name and value is byte response
	mu            sync.RWMutex
}

func NewNodeCache() *NodeCache {
	nc := &NodeCache{
		client:        &http.Client{},
		cacheResponse: make(map[string]JSONRPCResponse),
		mu:            sync.RWMutex{},
	}
	go nc.run()
	return nc
}

func (nc *NodeCache) run() {
	for _, method := range cacheMethods {
		go nc.cacheWorker(method)
	}
}

// cacheWorker A worker to serve a method
func (nc *NodeCache) cacheWorker(method string) {
	ticker := time.NewTicker(10 * time.Second)
	for {
		req, err := nc.makeRequest(method)
		if err != nil {
			log.Println(err)
			<-ticker.C
			continue
		}

		proxyReq, err := nc.cloneRequest(req)
		if err != nil {
			log.Println(err)
			<-ticker.C
			continue
		}

		resp, err := nc.callMethod(proxyReq)
		if err != nil {
			log.Println(err)
			<-ticker.C
			continue
		}

		jsonRPCResponse := JSONRPCResponse{}
		if err := json.Unmarshal(resp, &jsonRPCResponse); err != nil {
			log.Println(err)
			<-ticker.C
			continue
		}

		nc.SetCacheResponse(method, jsonRPCResponse)
		<-ticker.C
	}
}

// callMethod
func (nc *NodeCache) callMethod(req *http.Request) ([]byte, error) {
	// We may want to filter some headers, otherwise we could just use a shallow copy
	resp, err := nc.client.Do(req)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Print(err)
			return nil, err
		}
		return bodyBytes, nil
	}
	return nil, errors.New(fmt.Sprintf("Status code is %d", resp.StatusCode))
}

func (nc *NodeCache) makeRequest(method string) (*http.Request, error) {
	params := JSONRPCMessage{
		Version: "2.0",
		Method:  method,
		Params:  []string{},
	}

	paramBytes, err := json.Marshal(params)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	rbody := bytes.NewReader(paramBytes)

	req, err := http.NewRequest("POST", os.Getenv("NODE_ENDPOINT"), rbody)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	return req, nil
}

// SetCacheResponse Save method response to cache
func (nc *NodeCache) SetCacheResponse(method string, message JSONRPCResponse) {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	nc.cacheResponse[method] = message
}

// GetCacheResponse Get response from cache, return []byte
func (nc *NodeCache) GetCacheResponse(message JSONRPCMessage) ([]byte, error) {
	nc.mu.RLock()
	defer nc.mu.RUnlock()

	if jsonRPCResponse, ok := nc.cacheResponse[message.Method]; ok {
		// clone user request ID
		jsonRPCResponse.ID = message.ID
		result, err := json.Marshal(jsonRPCResponse)
		if err != nil {
			return []byte{}, err
		}
		return result, nil
	}
	return []byte{}, errors.New(fmt.Sprintf("Method %s is not supported caching", message.Method))
}

// HandleRequest Handle client request, if method is in cache list then get from cache
func (nc *NodeCache) HandleRequest(req *http.Request) ([]byte, error) {
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	//get message from request body
	message := JSONRPCMessage{}
	if err := json.Unmarshal(body, &message); err == nil {
		cacheResp, respErr := nc.GetCacheResponse(message)
		if respErr == nil {
			return cacheResp, nil
		}
	}

	// reassign again
	req.Body = ioutil.NopCloser(bytes.NewReader(body))

	proxyReq, err := nc.cloneRequest(req)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return nc.callMethod(proxyReq)
}

// cloneRequest
func (nc *NodeCache) cloneRequest(req *http.Request) (*http.Request, error) {
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	proxyReq, err := http.NewRequest(req.Method, os.Getenv("NODE_ENDPOINT"), bytes.NewReader(body))
	if err != nil {
		log.Print(err)
		return nil, err
	}

	proxyReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_11_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/56.0.2924.87 Safari/537.36")

	return proxyReq, nil
}
