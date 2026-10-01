package desktop

import "os"

// zoneIdentifier marks the file as from the Internet zone (Mark-of-the-Web).
const zoneIdentifier = "[ZoneTransfer]\r\nZoneId=3\r\n"

// writeQuarantine writes the Zone.Identifier alternate data stream.
func writeQuarantine(path string) error {
	return os.WriteFile(path+":Zone.Identifier", []byte(zoneIdentifier), 0o644)
}
