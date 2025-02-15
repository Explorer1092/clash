package convert

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

var enc = base64.StdEncoding

func DecodeBase64(buf []byte) ([]byte, error) {
	buff := bytes.TrimSpace(buf)
	dBuf := make([]byte, enc.DecodedLen(len(buff)))
	n, err := enc.Decode(dBuf, buff)
	if err != nil {
		return nil, err
	}

	return dBuf[:n], nil
}

func DecodeRawBase64(buf []byte) ([]byte, error) {
	buff := bytes.TrimSpace(buf)
	dBuf := make([]byte, base64.RawStdEncoding.DecodedLen(len(buff)))
	n, err := base64.RawStdEncoding.Decode(dBuf, buff)
	if err != nil {
		return nil, err
	}

	return dBuf[:n], nil
}

// ConvertsV2Ray convert V2Ray subscribe proxies data to clash proxies config
func ConvertsV2Ray(buf []byte) ([]map[string]any, error) {
	data, err := DecodeBase64(buf)
	if err != nil {
		data, err = DecodeRawBase64(buf)
		if err != nil {
			data = buf
		}
	}

	arr := strings.Split(string(data), "\n")

	proxies := make([]map[string]any, 0, len(arr))
	names := make(map[string]int, 200)

	for _, line := range arr {
		line = strings.TrimRight(line, " \r")
		if line == "" {
			continue
		}

		scheme, body, found := strings.Cut(line, "://")
		if !found {
			continue
		}

		scheme = strings.ToLower(scheme)
		switch scheme {
		case "trojan":
			urlTrojan, err := url.Parse(line)
			if err != nil {
				continue
			}

			query := urlTrojan.Query()

			name := uniqueName(names, urlTrojan.Fragment)
			trojan := make(map[string]any, 20)

			trojan["name"] = name
			trojan["type"] = scheme
			trojan["server"] = urlTrojan.Hostname()
			trojan["port"] = urlTrojan.Port()
			trojan["password"] = urlTrojan.User.Username()
			trojan["udp"] = true
			trojan["skip-cert-verify"] = false

			sni := query.Get("sni")
			if sni != "" {
				trojan["sni"] = sni
			}

			network := strings.ToLower(query.Get("type"))
			if network != "" {
				trojan["network"] = network
			}

			if network == "ws" {
				headers := make(map[string]any)
				wsOpts := make(map[string]any)

				headers["User-Agent"] = RandUserAgent()

				wsOpts["path"] = query.Get("path")
				wsOpts["headers"] = headers

				trojan["ws-opts"] = wsOpts
			}

			proxies = append(proxies, trojan)
		case "vmess":
			dcBuf, err := enc.DecodeString(body)
			if err != nil {
				continue
			}

			jsonDc := json.NewDecoder(bytes.NewReader(dcBuf))
			values := make(map[string]any, 20)

			if jsonDc.Decode(&values) != nil {
				continue
			}

			name, ok := values["ps"].(string)
			if !ok {
				continue
			}

			name = uniqueName(names, name)
			vmess := make(map[string]any, 20)

			vmess["name"] = name
			vmess["type"] = scheme
			vmess["server"] = values["add"]
			vmess["port"] = values["port"]
			vmess["uuid"] = values["id"]
			vmess["alterId"] = values["aid"]
			vmess["cipher"] = "auto"
			vmess["udp"] = true
			vmess["skip-cert-verify"] = false

			var (
				sni     = values["sni"]
				host    = values["host"]
				network = "tcp"
			)
			if n, ok := values["net"].(string); ok {
				network = strings.ToLower(n)
			}
			vmess["network"] = network

			var (
				tls   = ""
				isTls = false
			)
			if t, ok := values["tls"].(string); ok {
				tls = strings.ToLower(t)
			}
			if tls != "" && tls != "0" && tls != "null" {
				if sni != nil {
					vmess["servername"] = sni
				}
				vmess["tls"] = true
				isTls = true
			}

			if network == "ws" {
				headers := make(map[string]any)
				wsOpts := make(map[string]any)

				if !isTls {
					if _, ok = host.(string); ok {
						headers["Host"] = host
					} else {
						headers["Host"] = RandHost()
					}
				}

				headers["User-Agent"] = RandUserAgent()

				if values["path"] != nil {
					wsOpts["path"] = values["path"]
				}
				wsOpts["headers"] = headers

				vmess["ws-opts"] = wsOpts
			} else if network == "http" {
				headers := make(map[string][]string)
				httpOpts := make(map[string]any)

				if !isTls {
					if h, ok := host.(string); ok {
						headers["Host"] = []string{h}
					} else {
						headers["Host"] = []string{RandHost()}
					}
				}

				headers["User-Agent"] = []string{RandUserAgent()}

				if values["path"] != nil {
					httpOpts["path"] = values["path"]
				}
				httpOpts["Host"] = values["add"]
				httpOpts["headers"] = headers

				vmess["http-opts"] = httpOpts
			}

			proxies = append(proxies, vmess)
		case "ss":
			urlSS, err := url.Parse(line)
			if err != nil {
				continue
			}

			name := uniqueName(names, urlSS.Fragment)
			port := urlSS.Port()

			if port == "" {
				dcBuf, err := enc.DecodeString(urlSS.Host)
				if err != nil {
					continue
				}

				urlSS, err = url.Parse("ss://" + string(dcBuf))
				if err != nil {
					continue
				}
			}

			var (
				cipher   = urlSS.User.Username()
				password string
			)

			if password, found = urlSS.User.Password(); !found {
				dcBuf, err := enc.DecodeString(cipher)
				if err != nil {
					continue
				}

				cipher, password, found = strings.Cut(string(dcBuf), ":")
				if !found {
					continue
				}
			}

			ss := make(map[string]any, 20)

			ss["name"] = name
			ss["type"] = scheme
			ss["server"] = urlSS.Hostname()
			ss["port"] = urlSS.Port()
			ss["cipher"] = cipher
			ss["password"] = password
			ss["udp"] = true

			proxies = append(proxies, ss)
		case "ssr":
			dcBuf, err := enc.DecodeString(body)
			if err != nil {
				continue
			}

			// ssr://host:port:protocol:method:obfs:urlsafebase64pass/?obfsparam=urlsafebase64&protoparam=&remarks=urlsafebase64&group=urlsafebase64&udpport=0&uot=1

			before, after, ok := strings.Cut(string(dcBuf), "/?")
			if !ok {
				continue
			}

			beforeArr := strings.Split(before, ":")

			if len(beforeArr) != 6 {
				continue
			}

			host := beforeArr[0]
			port := beforeArr[1]
			protocol := beforeArr[2]
			method := beforeArr[3]
			obfs := beforeArr[4]
			password := decodeUrlSafe(urlSafe(beforeArr[5]))

			query, err := url.ParseQuery(urlSafe(after))
			if err != nil {
				continue
			}

			remarks := decodeUrlSafe(query.Get("remarks"))
			name := uniqueName(names, remarks)

			obfsParam := decodeUrlSafe(query.Get("obfsparam"))
			protocolParam := query.Get("protoparam")

			ssr := make(map[string]any, 20)

			ssr["name"] = name
			ssr["type"] = scheme
			ssr["server"] = host
			ssr["port"] = port
			ssr["cipher"] = method
			ssr["password"] = password
			ssr["obfs"] = obfs
			ssr["protocol"] = protocol
			ssr["udp"] = true

			if obfsParam != "" {
				ssr["obfs-param"] = obfsParam
			}

			if protocolParam != "" {
				ssr["protocol-param"] = protocolParam
			}

			proxies = append(proxies, ssr)
		case "vless":
			urlVless, err := url.Parse(line)
			if err != nil {
				continue
			}

			query := urlVless.Query()

			name := uniqueName(names, urlVless.Fragment)
			vless := make(map[string]any, 20)

			vless["name"] = name
			vless["type"] = scheme
			vless["server"] = urlVless.Hostname()
			vless["port"] = urlVless.Port()
			vless["uuid"] = urlVless.User.Username()
			vless["udp"] = true
			vless["skip-cert-verify"] = false

			sni := query.Get("sni")
			if sni != "" {
				vless["servername"] = sni
			}

			flow := strings.ToLower(query.Get("flow"))
			if flow != "" {
				vless["flow"] = flow
			}

			network := strings.ToLower(query.Get("type"))
			if network != "" {
				vless["network"] = network
			}

			if network == "ws" {
				headers := make(map[string]any)
				wsOpts := make(map[string]any)

				headers["User-Agent"] = RandUserAgent()

				wsOpts["path"] = query.Get("path")
				wsOpts["headers"] = headers

				vless["ws-opts"] = wsOpts
			}

			proxies = append(proxies, vless)
		}
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("convert v2ray subscribe error: format invalid")
	}

	return proxies, nil
}

func urlSafe(data string) string {
	return strings.ReplaceAll(strings.ReplaceAll(data, "+", "-"), "/", "_")
}

func decodeUrlSafe(data string) string {
	dcBuf, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		return ""
	}
	return string(dcBuf)
}

func uniqueName(names map[string]int, name string) string {
	if index, ok := names[name]; ok {
		index++
		names[name] = index
		name = fmt.Sprintf("%s-%02d", name, index)
	} else {
		index = 0
		names[name] = index
	}
	return name
}

func ConvertsWireGuard(buf []byte) ([]map[string]any, error) {
	var (
		proxies = make([]map[string]any, 0, 50)
		wgMap   map[string]any
		scanner = bufio.NewScanner(bytes.NewReader(buf))
	)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			line = strings.ToLower(strings.TrimSpace(line))
			if line == "[interface]" {
				if wgMap != nil && wgMap["name"] == nil {
					if pk, ok := wgMap["public-key"].(string); ok && len(pk) >= 8 {
						wgMap["name"] = fmt.Sprintf("wg-%s", pk[:8])
					}
				}
				wgMap = make(map[string]any, 12)
				wgMap["type"] = "wireguard"
				wgMap["dns"] = make([]string, 0, 5)
				wgMap["udp"] = true
				proxies = append(proxies, wgMap)
			}
			continue
		}

		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		switch key {
		case "name":
			wgMap["name"] = value
		case "endpoint":
			host, port, err := net.SplitHostPort(value)
			if err != nil {
				return nil, err
			}
			p, err := strconv.Atoi(port)
			if err != nil {
				return nil, err
			}
			wgMap["server"] = host
			wgMap["port"] = p
		case "address":
			ips := strings.Split(value, ",")
			for _, v := range ips {
				e, _, _ := strings.Cut(v, "/")
				ip, err := netip.ParseAddr(strings.TrimSpace(e))
				if err != nil {
					return nil, err
				}
				if ip.Is4() {
					wgMap["ip"] = ip.String()
				} else {
					wgMap["ipv6"] = ip.String()
				}
			}
		case "privatekey":
			wgMap["private-key"] = value
		case "publickey":
			wgMap["public-key"] = value
		case "presharedkey":
			wgMap["preshared-key"] = value
		case "dns":
			ips := strings.Split(value, ",")
			for _, v := range ips {
				v = strings.TrimSpace(v)
				ip, err := netip.ParseAddr(v)
				if err != nil {
					return nil, err
				}
				dnses := wgMap["dns"].([]string)
				wgMap["dns"] = append(dnses, ip.String())
			}
		case "mtu":
			v, err := strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
			wgMap["mtu"] = v
		}
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("convert WireGuard error: format invalid")
	}

	if pk, ok := wgMap["public-key"].(string); ok && wgMap["name"] == nil && len(pk) >= 8 {
		wgMap["name"] = fmt.Sprintf("wg-%s", pk[:8])
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return proxies, nil
}
