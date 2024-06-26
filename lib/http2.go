/*
2024 Moopinger
*/

package lib

import (
	"crypto/tls"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func generateSettingsFrame() []byte {

	header := []byte{
		0x00, 0x00, 0x18, // Length: 24 bytes (6 bytes for each setting)
		0x04,                   // Type: SETTINGS
		0x00,                   // Flags: NO_FLAGS
		0x00, 0x00, 0x00, 0x00, // Stream identifier: 0
	}

	payload := []byte{
		0x00, 0x02, 0x00, 0x00, 0x00, 0x00, // SETTINGS_ENABLE_PUSH: 0
		0x00, 0x03, 0x00, 0x00, 0x03, 0xE8, // SETTINGS_MAX_CONCURRENT_STREAMS: 1000
		0x00, 0x04, 0x00, 0x60, 0x00, 0x00, // SETTINGS_INITIAL_WINDOW_SIZE: 6291456
		0x00, 0x08, 0x00, 0x00, 0x00, 0x01, // SETTINGS_ENABLE_CONNECT_PROTOCOL: 1
	}

	return append(header, payload...)

}

func GenerateRequest(hostname string, path string, customHeaderName []byte, custonHeaderValue []byte, streamId uint32, requestMethod string, additionalHeader string, customDataFrame string) ([]byte, error) {

	//convert customHeaderName to string
	additionalHeaderName := []byte{}
	additionalHeaderValue := []byte{}

	if additionalHeader != "" {
		//check if additional headers contains string patters: ": " if not we return
		if !strings.Contains(additionalHeader, ": ") {
			return nil, fmt.Errorf("additional header does not contain a colon and space")
		}

		//split the header-name and header-value via the colon and space
		additionalHeaderName = []byte(additionalHeader[:strings.Index(additionalHeader, ": ")])
		additionalHeaderValue = []byte(additionalHeader[strings.Index(additionalHeader, ": ")+2:])

	}

	//if a user provides custom pseudo headers we want to withhold our own to prevent collisions
	var withholdScheme bool = false
	var withholdAuthority bool = false
	var withholdPath bool = false
	var withholdMethod bool = false
	var withholdUserAgent bool = false

	if len(customHeaderName) >= 7 && string(customHeaderName[:7]) == ":scheme" {
		withholdScheme = true
	} else if len(customHeaderName) >= 10 && string(customHeaderName[:10]) == ":authority" {
		withholdAuthority = true
	} else if len(customHeaderName) >= 5 && string(customHeaderName[:5]) == ":path" {
		withholdPath = true
	} else if len(customHeaderName) >= 7 && string(customHeaderName[:7]) == ":method" {
		withholdMethod = true
	} else if len(customHeaderName) >= 10 && string(customHeaderName[:10]) == "user-agent" {
		withholdUserAgent = true
	}

	header := []byte{
		0x00, 0x00, 0x00, // Length: will be set later
		0x01,                   // Type: HEADERS
		0x04,                   // Flags: END_HEADERS
		0x00, 0x00, 0x00, 0x00, // Stream identifier: 1
	}

	// Set the stream identifier
	header[5] = byte(streamId >> 24)
	header[6] = byte(streamId >> 16)
	header[7] = byte(streamId >> 8)
	header[8] = byte(streamId)

	//add the pseudo headers
	payload := []byte{}

	//if withholdscheme is false we add the scheme with 0x87
	if !withholdScheme {
		payload = append(payload, 0x87)
	}

	//if withholdAuthority is false we add the authority with 0x41 and the hostname length followed by the hostname
	if !withholdAuthority {

		payload = append(payload, 0x41, byte(len(hostname)))
		payload = append(payload, hostname...)
	}

	if !withholdMethod {
		methodHeader := append([]byte{0x42, byte(len(requestMethod))}, []byte(requestMethod)...) // 0x04 is the index for :path 0x40 for incremental indexing
		payload = append(payload, []byte(methodHeader)...)
	}

	if !withholdPath {

		pathHeader := append([]byte{0x44, byte(len(path))}, path...) // 0x04 is the index for :path 0x40 for incremental indexing
		payload = append(payload, pathHeader...)

	}

	// Add the custom header always
	customHeader := []byte{
		0x40,                        // Literal Header Field with Incremental Indexing
		byte(len(customHeaderName)), // Length of 'customHeaderName'
	}

	customHeader = append(customHeader, customHeaderName...)
	customHeader = append(customHeader, byte(len(custonHeaderValue)))
	customHeader = append(customHeader, custonHeaderValue...)

	payload = append(payload, customHeader...)

	if additionalHeader != "" {

		additionalCustomHeader := []byte{
			0x40,                            // Literal Header Field with Incremental Indexing
			byte(len(additionalHeaderName)), // Length of 'customHeaderName'
		}

		additionalCustomHeader = append(additionalCustomHeader, additionalHeaderName...)
		additionalCustomHeader = append(additionalCustomHeader, byte(len(additionalHeaderValue)))
		additionalCustomHeader = append(additionalCustomHeader, additionalHeaderValue...)

		payload = append(payload, additionalCustomHeader...)
	}

	//user agent
	if !withholdUserAgent {

		userAgentHeader := []byte{
			0x40,                    // Literal Header Field with Incremental Indexing
			byte(len("user-agent")), // Length of 'user-agent'
		}

		userAgentHeader = append(userAgentHeader, "user-agent"...)
		userAgentHeader = append(userAgentHeader, byte(len(UserAgentHeaderValue)))
		userAgentHeader = append(userAgentHeader, UserAgentHeaderValue...)

		payload = append(payload, userAgentHeader...)
	}

	//accept: */*

	acceptHeader := []byte{
		0x40,                // Literal Header Field with Incremental Indexing
		byte(len("accept")), // Length of 'user-agent'
	}

	acceptHeader = append(acceptHeader, "accept"...)
	acceptHeader = append(acceptHeader, byte(len("*/*")))
	acceptHeader = append(acceptHeader, "*/*"...)

	payload = append(payload, acceptHeader...)

	//Set the length of the payload

	header[0] = byte(len(payload) >> 16)
	header[1] = byte(len(payload) >> 8)
	header[2] = byte(len(payload))

	//append the payload to the header without returning
	header = append(header, payload...)

	//create a data frame with the same stream id and the END_STREAM flag set
	//333 will be the smuggled TE value hopefully causing a timeout
	//If its a confirmation request we modify the body to 3\r\nABC\r\n0\r\n\r\n
	//var dataFrame []byte
	dataFrame := generateDataFrame(streamId, customDataFrame)

	//append the data frame to the header
	header = append(header, dataFrame...)
	return header, nil

}

func generateDataFrame(streamId uint32, data string) []byte {

	header := []byte{
		0x00, 0x00, 0x00, // Length: will be set later
		0x00,                   // Type: DATA
		0x01,                   // Flags: END_STREAM
		0x00, 0x00, 0x00, 0x00, // Stream id will be set later
	}

	// Set the stream identifier
	header[5] = byte(streamId >> 24)
	header[6] = byte(streamId >> 16)
	header[7] = byte(streamId >> 8)
	header[8] = byte(streamId)

	//convert the string to a byte array
	payload := []byte(data)

	//Set the length of the payload
	header[0] = byte(len(payload) >> 16)
	header[1] = byte(len(payload) >> 8)
	header[2] = byte(len(payload))

	//append the payload to the header without returning
	return append(header, payload...)

}

func HandleConnection(scanJob *ScanJob, streamChan *chan string) {

	framer := http2.NewFramer(scanJob.Conn, scanJob.Conn)
	hpackstatus := ""
	hpackBody := ""
	hpackDecoder := hpack.NewDecoder(4096, func(hf hpack.HeaderField) {
		if hf.Name == ":status" {
			hpackstatus = hf.Value
		}
	})

	//create a map to store the stream ids and the corresponding data frame size
	streamContentCount := make(map[uint32]int)

	for scanJob.Conn != nil {

		frame, err := framer.ReadFrame()

		if err != nil {
			if err == io.EOF {
				fmt.Println("[-] Server unexpectedly closed the connection, Is there a WAF? Results may be inaccurate.")
				return
			}
			//Enable this for debugging
			//fmt.Println("Error reading from connection:", err)
			return
		}

		streamId := uint32(frame.Header().StreamID)

		switch frame.(type) {
		case *http2.HeadersFrame:

			_, err := hpackDecoder.Write(frame.(*http2.HeadersFrame).HeaderBlockFragment())

			if err != nil {

				fmt.Println("Error writing to hpack decoder:", err)

			}
			err = hpackDecoder.Close()
			if err != nil {

				fmt.Println("Error closing hpack decoder:", err)

			}

			if frame.(*http2.HeadersFrame).Flags == 0x5 {

				//check if the streamContentCount is 0

				if streamId == scanJob.StreamId {

					*streamChan <- "SUCCESS [" + hpackstatus + "] Length: 0"
				}

			}
		case *http2.GoAwayFrame:

			*streamChan <- "GOAWAY"

			return

		case *http2.RSTStreamFrame:

			errorCode := (frame.(*http2.RSTStreamFrame).ErrCode.String())

			if streamId == scanJob.StreamId && errorCode != "NO_ERROR" {

				*streamChan <- "RST_STREAM: " + errorCode
			}

		case *http2.DataFrame:

			size := len(frame.(*http2.DataFrame).Data())
			streamContentCount[streamId] += size

			hpackBody += string(frame.(*http2.DataFrame).Data())

			//check if END_STREAM flag is set
			if frame.(*http2.DataFrame).Flags == 0x1 {

				if streamId == scanJob.StreamId {

					if scanJob.Keyword != "" {
						if strings.Contains(hpackBody, scanJob.Keyword) {
							*streamChan <- "SUCCESS [" + hpackstatus + "] - Keyword: True - Length: " + fmt.Sprintf("%d", streamContentCount[streamId])

						} else {
							*streamChan <- "SUCCESS [" + hpackstatus + "] - Keyword: False - Length: " + fmt.Sprintf("%d", streamContentCount[streamId])
						}

					} else {

						*streamChan <- "SUCCESS [" + hpackstatus + "] Length: " + fmt.Sprintf("%d", streamContentCount[streamId])

					}

				}

				hpackBody = ""

			}

		case *http2.SettingsFrame:
			//fmt.Println("SETTINGS Received")
		default:
			//fmt.Println("Unknown Frame Received")
		}

	}

}

func SendCustomFrame(frame []byte, conn *tls.Conn) error {

	if conn == nil {
		return fmt.Errorf("connection is nil")
	}

	_, err := conn.Write(frame)
	if err != nil {
		return err
	}
	return nil
}

func sendMagicReq(conn *tls.Conn) error {

	if conn == nil {
		return fmt.Errorf("connection is nil")
	}

	_, err := conn.Write([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	if err != nil {
		return err
	}
	return nil
}

func EstablishH2Connection(conn *tls.Conn) error {

	//fmt.Print("[+]MagicReq Sent\n")

	err := sendMagicReq(conn)

	if err != nil {
		return err

	}

	//fmt.Print("[+]Sending Settings Frame\n")

	err = SendCustomFrame(generateSettingsFrame(), conn)

	if err != nil {
		return err
	}

	//send window update frame
	//fmt.Print("[+]Sending Window Update Frame\n")
	err = SendCustomFrame(generateWindowUpdateFrame(0x00), conn)
	if err != nil {
		return err
	}

	return nil

}

func generateWindowUpdateFrame(streamId uint32) []byte {
	header := []byte{
		0x00, 0x00, 0x04, // Length: 4 bytes
		0x08,                   // Type: WINDOW_UPDATE
		0x00,                   // Flags: NO_FLAGS
		0x00, 0x00, 0x00, 0x00, // Stream identifier: streamId
	}

	// Set the stream identifier
	header[5] = byte(streamId >> 24)
	header[6] = byte(streamId >> 16)
	header[7] = byte(streamId >> 8)
	header[8] = byte(streamId)

	payload := []byte{
		0x7F, 0x0F, 0xFF, 0xFF, // Window Size Increment: 32767
	}

	return append(header, payload...)
}
