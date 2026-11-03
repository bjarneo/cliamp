// CliampTap reads the audio output of one process with a muted Core Audio
// process tap and streams it to a Unix socket.
//
// Usage: CliampTap <socket path> <pid>
//
// Wire format: one little-endian float64 sample rate, then interleaved stereo
// float32 frames until either side closes. Audio goes only to that socket.
import CoreAudio
import Foundation

func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(1)
}

guard CommandLine.arguments.count == 3, var pid = pid_t(CommandLine.arguments[2]) else {
    fail("usage: CliampTap <socket path> <pid>")
}
let socketPath = CommandLine.arguments[1]

func connectSocket(_ path: String) -> Int32 {
    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    guard fd >= 0 else { fail("socket failed") }
    var noSigPipe: Int32 = 1
    setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &noSigPipe, socklen_t(MemoryLayout<Int32>.size))
    var addr = sockaddr_un()
    addr.sun_family = sa_family_t(AF_UNIX)
    let bytes = Array(path.utf8)
    guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else { fail("socket path too long") }
    withUnsafeMutableBytes(of: &addr.sun_path) { $0.copyBytes(from: bytes) }
    let ok = withUnsafePointer(to: &addr) {
        $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
        }
    }
    guard ok == 0 else { fail("connect failed") }
    return fd
}

let fd = connectSocket(socketPath)

func writeAll(_ p: UnsafeRawPointer, _ count: Int) {
    var done = 0
    while done < count {
        let n = write(fd, p + done, count - done)
        if n <= 0 {
            if n < 0 && errno == EINTR { continue }
            exit(0) // cliamp went away; the tap ends with this process.
        }
        done += n
    }
}

var object = AudioObjectID(0)
var translate = AudioObjectPropertyAddress(
    mSelector: kAudioHardwarePropertyTranslatePIDToProcessObject,
    mScope: kAudioObjectPropertyScopeGlobal,
    mElement: kAudioObjectPropertyElementMain)
var objectSize = UInt32(MemoryLayout<AudioObjectID>.size)
let translated = AudioObjectGetPropertyData(
    AudioObjectID(kAudioObjectSystemObject), &translate,
    UInt32(MemoryLayout<pid_t>.size), &pid, &objectSize, &object)
guard translated == noErr, object != 0 else { fail("pid has no audio object: \(translated)") }

let description = CATapDescription(stereoMixdownOfProcesses: [object])
description.uuid = UUID()
description.isPrivate = true
description.muteBehavior = .mutedWhenTapped

var tap = AudioObjectID(0)
let tapStatus = AudioHardwareCreateProcessTap(description, &tap)
guard tapStatus == noErr else { fail("tap creation failed: \(tapStatus)") }

var format = AudioStreamBasicDescription()
var formatSize = UInt32(MemoryLayout<AudioStreamBasicDescription>.size)
var formatAddress = AudioObjectPropertyAddress(
    mSelector: kAudioTapPropertyFormat,
    mScope: kAudioObjectPropertyScopeGlobal,
    mElement: kAudioObjectPropertyElementMain)
let formatStatus = AudioObjectGetPropertyData(tap, &formatAddress, 0, nil, &formatSize, &format)
guard formatStatus == noErr else { fail("tap format failed: \(formatStatus)") }

let aggregate: [String: Any] = [
    kAudioAggregateDeviceNameKey: "cliamp-apple-music",
    kAudioAggregateDeviceUIDKey: UUID().uuidString,
    kAudioAggregateDeviceIsPrivateKey: true,
    kAudioAggregateDeviceTapAutoStartKey: true,
    kAudioAggregateDeviceTapListKey: [[
        kAudioSubTapUIDKey: description.uuid.uuidString,
        kAudioSubTapDriftCompensationKey: true,
    ]],
]
var device = AudioObjectID(0)
let aggregateStatus = AudioHardwareCreateAggregateDevice(aggregate as CFDictionary, &device)
guard aggregateStatus == noErr else { fail("aggregate failed: \(aggregateStatus)") }

var rate = format.mSampleRate
withUnsafeBytes(of: &rate) { writeAll($0.baseAddress!, 8) }

var proc: AudioDeviceIOProcID?
let queue = DispatchQueue(label: "cliamp.tap")
let ioStatus = AudioDeviceCreateIOProcIDWithBlock(&proc, device, queue) { _, input, _, _, _ in
    for buffer in UnsafeMutableAudioBufferListPointer(UnsafeMutablePointer(mutating: input)) {
        guard let data = buffer.mData, buffer.mDataByteSize > 0 else { continue }
        writeAll(data, Int(buffer.mDataByteSize))
    }
}
guard ioStatus == noErr, let proc else { fail("ioproc failed: \(ioStatus)") }
guard AudioDeviceStart(device, proc) == noErr else { fail("device start failed") }

// Removing the process tears down the tap, so Music is never left muted.
dispatchMain()
