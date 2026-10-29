#if DEBUG
import Foundation
import Photos
import ImageIO
import AVFoundation
import CoreImage
import UniformTypeIdentifiers
import UIKit

/// Puts what an end-to-end run of auto-upload needs into the simulator's photo library:
/// a Live Photo, an edited photo, a second edit of it, its revert. `simctl addmedia`
/// makes plain files only. Debug only; driven by DISCODRIVE_TEST_SEED (comma-separated).
enum E2ESeed {
    private static let editedKey = "e2e.edited"

    static func run(_ steps: String) async -> String {
        var out: [String] = []
        for step in steps.split(separator: ",").map({ $0.trimmingCharacters(in: .whitespaces) }) {
            do {
                switch step {
                case "live": out.append("live=" + (try await live()))
                case "edit": out.append("edit=" + (try await editedPhoto()))
                case "reedit": out.append("reedit=" + (try await edit(try stored(), tag: "second")))
                case "revert": out.append("revert=" + (try await revert(try stored())))
                default: out.append("\(step)=unknown")
                }
            } catch { out.append("\(step)=error:\(error)") }
        }
        return out.joined(separator: " ")
    }

    // MARK: Live Photo

    /// A still and a video sharing a content identifier: Photos keeps them as one Live Photo.
    private static func live() async throws -> String {
        let cid = UUID().uuidString
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("e2e-" + cid)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let still = dir.appendingPathComponent("IMG_LIVE.JPG")
        try jpeg(color: .random, to: still, appleCID: cid)
        let movie = dir.appendingPathComponent("IMG_LIVE.MOV")
        try await mov(to: movie, cid: cid)
        var id = ""
        try await PHPhotoLibrary.shared().performChanges {
            let req = PHAssetCreationRequest.forAsset()
            let opts = PHAssetResourceCreationOptions()
            opts.originalFilename = "IMG_LIVE.JPG"
            req.addResource(with: .photo, fileURL: still, options: opts)
            let vopts = PHAssetResourceCreationOptions()
            vopts.originalFilename = "IMG_LIVE.MOV"
            req.addResource(with: .pairedVideo, fileURL: movie, options: vopts)
            id = req.placeholderForCreatedAsset?.localIdentifier ?? ""
        }
        let asset = PHAsset.fetchAssets(withLocalIdentifiers: [id], options: nil).firstObject
        return asset.map { $0.mediaSubtypes.contains(.photoLive) ? "ok" : "not-live" } ?? "missing"
    }

    private static func jpeg(color: UIColor, to url: URL, appleCID: String? = nil) throws {
        let image = UIGraphicsImageRenderer(size: CGSize(width: 320, height: 240)).image { ctx in
            color.setFill(); ctx.fill(CGRect(x: 0, y: 0, width: 320, height: 240))
        }
        guard let cg = image.cgImage,
              let dest = CGImageDestinationCreateWithURL(url as CFURL, UTType.jpeg.identifier as CFString, 1, nil) else {
            throw CocoaError(.fileWriteUnknown)
        }
        var props: [CFString: Any] = [:]
        if let appleCID { props[kCGImagePropertyMakerAppleDictionary] = ["17": appleCID] }
        CGImageDestinationAddImage(dest, cg, props as CFDictionary)
        guard CGImageDestinationFinalize(dest) else { throw CocoaError(.fileWriteUnknown) }
    }

    /// One second of a flat colour, with the content identifier and the still-image time.
    private static func mov(to url: URL, cid: String) async throws {
        let writer = try AVAssetWriter(outputURL: url, fileType: .mov)
        let id = AVMutableMetadataItem()
        id.keySpace = .quickTimeMetadata
        id.key = "com.apple.quicktime.content.identifier" as NSString
        id.value = cid as NSString
        id.dataType = "com.apple.metadata.datatype.UTF-8"
        writer.metadata = [id]

        let video = AVAssetWriterInput(mediaType: .video, outputSettings: [
            AVVideoCodecKey: AVVideoCodecType.h264, AVVideoWidthKey: 320, AVVideoHeightKey: 240])
        video.expectsMediaDataInRealTime = false
        let pixels = AVAssetWriterInputPixelBufferAdaptor(assetWriterInput: video, sourcePixelBufferAttributes: [
            kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA,
            kCVPixelBufferWidthKey as String: 320, kCVPixelBufferHeightKey as String: 240])
        writer.add(video)

        let spec: [String: Any] = [
            kCMMetadataFormatDescriptionMetadataSpecificationKey_Identifier as String: "mdta/com.apple.quicktime.still-image-time",
            kCMMetadataFormatDescriptionMetadataSpecificationKey_DataType as String: "com.apple.metadata.datatype.int8"]
        var desc: CMFormatDescription?
        CMMetadataFormatDescriptionCreateWithMetadataSpecifications(allocator: kCFAllocatorDefault,
            metadataType: kCMMetadataFormatType_Boxed, metadataSpecifications: [spec] as CFArray, formatDescriptionOut: &desc)
        let meta = AVAssetWriterInput(mediaType: .metadata, outputSettings: nil, sourceFormatHint: desc)
        let metaAdaptor = AVAssetWriterInputMetadataAdaptor(assetWriterInput: meta)
        writer.add(meta)

        guard writer.startWriting() else { throw writer.error ?? CocoaError(.fileWriteUnknown) }
        writer.startSession(atSourceTime: .zero)
        let still = AVMutableMetadataItem()
        still.key = "com.apple.quicktime.still-image-time" as NSString
        still.keySpace = .quickTimeMetadata
        still.value = 0 as NSNumber
        still.dataType = "com.apple.metadata.datatype.int8"
        metaAdaptor.append(AVTimedMetadataGroup(items: [still],
            timeRange: CMTimeRange(start: CMTime(value: 15, timescale: 30), duration: CMTime(value: 1, timescale: 30))))
        meta.markAsFinished()

        for frame in 0..<30 {
            while !video.isReadyForMoreMediaData { try await Task.sleep(for: .milliseconds(5)) }
            var buffer: CVPixelBuffer?
            CVPixelBufferPoolCreatePixelBuffer(nil, pixels.pixelBufferPool!, &buffer)
            guard let buffer else { throw CocoaError(.fileWriteUnknown) }
            CVPixelBufferLockBaseAddress(buffer, [])
            memset(CVPixelBufferGetBaseAddress(buffer), Int32(frame * 8), CVPixelBufferGetDataSize(buffer))
            CVPixelBufferUnlockBaseAddress(buffer, [])
            pixels.append(buffer, withPresentationTime: CMTime(value: CMTimeValue(frame), timescale: 30))
        }
        video.markAsFinished()
        await writer.finishWriting()
        if writer.status != .completed { throw writer.error ?? CocoaError(.fileWriteUnknown) }
    }

    // MARK: edits

    private static func editedPhoto() async throws -> String {
        let dir = FileManager.default.temporaryDirectory
        let file = dir.appendingPathComponent("IMG_EDIT-\(UUID().uuidString).JPG")
        defer { try? FileManager.default.removeItem(at: file) }
        try jpeg(color: .random, to: file)
        var id = ""
        try await PHPhotoLibrary.shared().performChanges {
            let req = PHAssetCreationRequest.forAsset()
            let opts = PHAssetResourceCreationOptions()
            opts.originalFilename = "IMG_EDIT.JPG"
            req.addResource(with: .photo, fileURL: file, options: opts)
            id = req.placeholderForCreatedAsset?.localIdentifier ?? ""
        }
        UserDefaults.standard.set(id, forKey: editedKey)
        return try await edit(id, tag: "first")
    }

    private static func stored() throws -> String {
        guard let id = UserDefaults.standard.string(forKey: editedKey) else { throw CocoaError(.fileNoSuchFile) }
        return id
    }

    /// An adjustment the way an editing app makes one: a rendered JPEG and its recipe.
    private static func edit(_ id: String, tag: String) async throws -> String {
        guard let asset = PHAsset.fetchAssets(withLocalIdentifiers: [id], options: nil).firstObject else { return "missing" }
        let opts = PHContentEditingInputRequestOptions()
        opts.canHandleAdjustmentData = { _ in false }
        // PhotoKit's input is not Sendable; it only crosses back to this task.
        final class Box: @unchecked Sendable { var input: PHContentEditingInput? }
        let box = Box()
        await withCheckedContinuation { (cont: CheckedContinuation<Void, Never>) in
            asset.requestContentEditingInput(with: opts) { input, _ in box.input = input; cont.resume() }
        }
        guard let input = box.input, let source = input.fullSizeImageURL, let image = CIImage(contentsOf: source) else { return "no-input" }
        let output = PHContentEditingOutput(contentEditingInput: input)
        output.adjustmentData = PHAdjustmentData(formatIdentifier: "org.discodrive.e2e", formatVersion: "1",
                                                 data: Data((tag + UUID().uuidString).utf8))
        let tinted = image.applyingFilter("CIColorMonochrome", parameters: [
            kCIInputColorKey: CIColor(color: .random), kCIInputIntensityKey: 1.0])
        try CIContext().writeJPEGRepresentation(of: tinted, to: output.renderedContentURL,
                                                 colorSpace: CGColorSpace(name: CGColorSpace.sRGB)!)
        try await PHPhotoLibrary.shared().performChanges {
            PHAssetChangeRequest(for: asset).contentEditingOutput = output
        }
        return "ok"
    }

    private static func revert(_ id: String) async throws -> String {
        guard let asset = PHAsset.fetchAssets(withLocalIdentifiers: [id], options: nil).firstObject else { return "missing" }
        try await PHPhotoLibrary.shared().performChanges {
            PHAssetChangeRequest(for: asset).revertAssetContentToOriginal()
        }
        return "ok"
    }
}

private extension UIColor {
    static var random: UIColor {
        UIColor(red: .random(in: 0...1), green: .random(in: 0...1), blue: .random(in: 0...1), alpha: 1)
    }
}
#endif
