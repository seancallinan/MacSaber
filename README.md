# MacSaber

This is a re-implementation of an application which existed for older Macs with a 'Sudden Motion Sensor' (SMS) - https://github.com/ianmaddox/MacSaber - which played lightsaber-like sound effects based on the motion of a MacBook.

It leverages https://github.com/taigrr/apple-silicon-accelerometer to provide access to the undocumented Bosch BMI286 IMU and other sensors exposed through Apple's AppleSPUHIDDevice IOKit interface on Apple Silicon MacBooks.

## Requirements
* macOS on Apple Silicon (M2 and newer)
* Go 1.26 or later (to build).
* Root privileges for access to AppleSPUHIDDevice IOKit interface.

## Install
Download the latest release from https://github.com/seancallinan/MacSaber/releases/latest

Or build from source:
`go install github.com/seancallinan/MacSaber@latest`

## Usage
`sudo ./MacSaber` or `sudo MacSaber` to run.

## Credits
* https://github.com/taigrr/apple-silicon-accelerometer for the Go library to access sensor data.
* https://github.com/olvvier/apple-silicon-accelerometer for the original Python library for accessing sensor data.
* https://github.com/taigrr/spank for a reference implementation of using the Go library.
* https://github.com/ianmaddox/MacSaber for the original project/concept.

## License
MIT