package upnp

import "testing"

func TestHeader(t *testing.T) {
	r := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nLocation: http://192.168.178.1:49000/igddesc.xml\r\n\r\n"
	if header(r, "location") != "http://192.168.178.1:49000/igddesc.xml" {
		t.Fatal(header(r, "location"))
	}
}

func TestCleanName(t *testing.T) {
	if n := cleanName("InternetGatewayDeviceV2 - FRITZ!Box 6690 Cable"); n != "FRITZ!Box 6690 Cable" {
		t.Fatal(n)
	}
	if n := cleanName("Speedport - Smart 4"); n != "Speedport - Smart 4" {
		t.Fatal(n)
	}
}
