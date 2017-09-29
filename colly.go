// Package colly implements a HTTP scraping framework
package colly

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"github.com/PuerkitoBio/goquery"
)

// Collector provides the scraper instance for a scraping job
type Collector struct {
	// UserAgent is the User-Agent string used by HTTP requests
	UserAgent string
	// MaxDepth limits the recursion depth of visited URLs.
	// Set it to 0 for infinite recursion (default).
	MaxDepth          int
	visitedURLs       []string
	htmlCallbacks     map[string]HTMLCallback
	requestCallbacks  []RequestCallback
	responseCallbacks []ResponseCallback
	client            *http.Client
	wg                *sync.WaitGroup
	lock              *sync.Mutex
}

// Request is the representation of a HTTP request made by a Collector
type Request struct {
	// URL is the parsed URL of the HTTP request
	URL *url.URL
	// Headers contains the Request's HTTP headers
	Headers *http.Header
	// CookieJar contains the Request's cookies
	CookieJar *cookiejar.Jar
	// Ctx is a context between a Request and a Response
	Ctx *Context
	// Depth is the number of the parents of this request
	Depth     int
	collector *Collector
}

// Response is the representation of a HTTP response made by a Collector
type Response struct {
	// StatusCode is the status code of the Response
	StatusCode int
	// Body is the content of the Response
	Body []byte
	// Ctx is a context between a Request and a Response
	Ctx *Context
	// Request is the Request object of the response
	Request *Request
}

// HTMLElement is the representation of a HTML tag.
type HTMLElement struct {
	// Name is the name of the tag
	Name       string
	attributes []html.Attribute
	// Request is the request object of the element's HTML document
	Request *Request
	// Response is the Response object of the element's HTML document
	Response *Response
}

// Context provides a tiny layer for passing data between different methods
type Context struct {
	contextMap map[string]string
	lock       *sync.Mutex
}

// RequestCallback is a type alias for OnRequest callback functions
type RequestCallback func(*Request)

// ResponseCallback is a type alias for OnResponse callback functions
type ResponseCallback func(*Response)

// HTMLCallback is a type alias for OnHTML callback functions
type HTMLCallback func(*HTMLElement)

// NewCollector creates a new Collector instance with default configuration
func NewCollector() *Collector {
	c := &Collector{}
	c.Init()
	return c
}

// NewCollector initializes a new Context instance
func NewContext() *Context {
	return &Context{
		contextMap: make(map[string]string),
		lock:       &sync.Mutex{},
	}
}

// Init initializes the Collector's private variables and sets default
// configuration for the Collector
func (c *Collector) Init() {
	c.UserAgent = "colly - https://github.com/asciimoo/colly"
	c.MaxDepth = 0
	c.visitedURLs = make([]string, 0, 8)
	c.htmlCallbacks = make(map[string]HTMLCallback, 0)
	c.requestCallbacks = make([]RequestCallback, 0, 8)
	c.responseCallbacks = make([]ResponseCallback, 0, 8)
	jar, _ := cookiejar.New(nil)
	c.client = &http.Client{
		Jar: jar,
	}
	c.wg = &sync.WaitGroup{}
	c.lock = &sync.Mutex{}
}

// Visit starts Collector's collecting job by creating a
// request to the URL specified in parameter.
// Visit also calls the previously provided OnRequest,
// OnResponse, OnHTML callbacks
func (c *Collector) Visit(u string) error {
	return c.scrape(u, 1)
}

func (c *Collector) scrape(u string, depth int) error {
	c.wg.Add(1)
	defer c.wg.Done()
	if u == "" {
		return nil
	}
	if c.MaxDepth > 0 && c.MaxDepth < depth {
		return nil
	}
	visited := false
	for _, u2 := range c.visitedURLs {
		if u2 == u {
			visited = true
			break
		}
	}
	if visited {
		return nil
	}
	c.lock.Lock()
	c.visitedURLs = append(c.visitedURLs, u)
	c.lock.Unlock()
	parsedURL, err := url.Parse(u)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	ctx := NewContext()
	request := &Request{
		URL:       parsedURL,
		Headers:   &req.Header,
		Ctx:       ctx,
		Depth:     depth,
		collector: c,
	}
	if len(c.requestCallbacks) > 0 {
		c.handleOnRequest(request)
	}
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}
	res.Body.Close()
	response := &Response{
		StatusCode: res.StatusCode,
		Body:       body,
		Ctx:        ctx,
	}
	if strings.Index(strings.ToLower(res.Header.Get("Content-Type")), "html") > -1 {
		c.handleOnHTML(body, request, response)
	}
	if len(c.responseCallbacks) > 0 {
		c.handleOnResponse(response)
	}
	return nil
}

// Wait returns when the collector jobs are finished
func (c *Collector) Wait() {
	c.wg.Done()
}

// OnRequest registers a function. Function will be executed on every
// request made by the Collector
func (c *Collector) OnRequest(f RequestCallback) {
	c.lock.Lock()
	c.requestCallbacks = append(c.requestCallbacks, f)
	c.lock.Unlock()
}

// OnRequest registers a function. Function will be executed on every response
func (c *Collector) OnResponse(f ResponseCallback) {
	c.lock.Lock()
	c.responseCallbacks = append(c.responseCallbacks, f)
	c.lock.Unlock()
}

// OnHTML registers a function. Function will be executed on every HTML
// element matched by the `query` parameter
func (c *Collector) OnHTML(goquerySelector string, f HTMLCallback) {
	c.lock.Lock()
	c.htmlCallbacks[goquerySelector] = f
	c.lock.Unlock()
}

// DisableCookies turns off cookie handling for this collector
func (c *Collector) DisableCookies() {
	c.client.Jar = nil
}

func (c *Collector) handleOnRequest(r *Request) {
	for _, f := range c.requestCallbacks {
		f(r)
	}
}

func (c *Collector) handleOnResponse(r *Response) {
	for _, f := range c.responseCallbacks {
		f(r)
	}
}

func (c *Collector) handleOnHTML(body []byte, req *Request, resp *Response) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewBuffer(body))
	if err != nil {
		return
	}
	for expr, f := range c.htmlCallbacks {
		doc.Find(expr).Each(func(i int, s *goquery.Selection) {
			for _, n := range s.Nodes {
				f(&HTMLElement{
					Name:       n.Data,
					Request:    req,
					Response:   resp,
					attributes: n.Attr,
				})
			}
		})
	}
}

// Attr returns the selected attribute of a HTMLElement or empty string
// if no attribute found
func (h *HTMLElement) Attr(k string) string {
	for _, a := range h.attributes {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

// AbsoluteURL returns with the resolved absolute URL of an URL chunk.
// AbsoluteURL retursn empty string if the URL chunk is a fragment or
// could not be parsed
func (r *Request) AbsoluteURL(u string) string {
	if strings.HasPrefix(u, "#") {
		return ""
	}
	absURL, err := r.URL.Parse(u)
	if err != nil {
		return ""
	}
	absURL.Fragment = ""
	if absURL.Scheme == "//" {
		absURL.Scheme = r.URL.Scheme
	}
	return absURL.String()
}

// Visit continues Collector's collecting job by creating a
// request to the URL specified in parameter.
// Visit also calls the previously provided OnRequest,
// OnResponse, OnHTML callbacks
func (r *Request) Visit(u string) error {
	return r.collector.scrape(r.AbsoluteURL(u), r.Depth+1)
}

// Put stores a value in Context
func (c *Context) Put(k, v string) {
	c.lock.Lock()
	c.contextMap[k] = v
	c.lock.Unlock()
}

// Get retrieves a value from Context. If no value found for `k`
// Get returns an empty string if key not found
func (c *Context) Get(k string) string {
	if v, ok := c.contextMap[k]; ok {
		return v
	}
	return ""
}
