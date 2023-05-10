// Code generated by "stringer -type=deviceSource"; DO NOT EDIT.

package main

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[IMAGE-0]
	_ = x[VIDEO-1]
	_ = x[STREAM-2]
}

const _deviceSource_name = "IMAGEVIDEOSTREAM"

var _deviceSource_index = [...]uint8{0, 5, 10, 16}

func (i deviceSource) String() string {
	if i < 0 || i >= deviceSource(len(_deviceSource_index)-1) {
		return "deviceSource(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _deviceSource_name[_deviceSource_index[i]:_deviceSource_index[i+1]]
}
