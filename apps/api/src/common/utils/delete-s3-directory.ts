/*
 * Copyright 2025 Daytona Platforms Inc.
 * SPDX-License-Identifier: AGPL-3.0
 */

import {
  S3Client,
  ListObjectsV2Command,
  DeleteObjectsCommand,
  ListObjectVersionsCommand,
} from '@aws-sdk/client-s3'

/**
 * Delete all objects under a specific prefix (directory) in an S3 bucket
 * @param s3 - S3 client instance
 * @param bucket - Bucket name
 * @param prefix - Prefix (directory path) to delete, should end with '/'
 */
export async function deleteS3Directory(s3: S3Client, bucket: string, prefix: string): Promise<void> {
  // Ensure prefix ends with '/' for proper directory matching
  const normalizedPrefix = prefix.endsWith('/') ? prefix : `${prefix}/`

  // First delete all object versions & delete markers (if any exist)
  let keyMarker: string | undefined
  let versionIdMarker: string | undefined
  do {
    const versions = await s3.send(
      new ListObjectVersionsCommand({
        Bucket: bucket,
        Prefix: normalizedPrefix,
        KeyMarker: keyMarker,
        VersionIdMarker: versionIdMarker,
      }),
    )
    const items = [
      ...(versions.Versions || [])
        .filter((v) => v.Key && v.Key.startsWith(normalizedPrefix))
        .map((v) => ({ Key: v.Key, VersionId: v.VersionId })),
      ...(versions.DeleteMarkers || [])
        .filter((d) => d.Key && d.Key.startsWith(normalizedPrefix))
        .map((d) => ({ Key: d.Key, VersionId: d.VersionId })),
    ]
    if (items.length) {
      await s3.send(
        new DeleteObjectsCommand({
          Bucket: bucket,
          Delete: { Objects: items, Quiet: true },
        }),
      )
    }
    keyMarker = versions.NextKeyMarker
    versionIdMarker = versions.NextVersionIdMarker
  } while (keyMarker || versionIdMarker)

  // Then delete any remaining live objects (for unversioned buckets)
  let continuationToken: string | undefined
  do {
    const list = await s3.send(
      new ListObjectsV2Command({
        Bucket: bucket,
        Prefix: normalizedPrefix,
        ContinuationToken: continuationToken,
      }),
    )
    if (list.Contents && list.Contents.length) {
      await s3.send(
        new DeleteObjectsCommand({
          Bucket: bucket,
          Delete: {
            Objects: list.Contents.map((o) => ({ Key: o.Key })),
            Quiet: true,
          },
        }),
      )
    }
    continuationToken = list.NextContinuationToken
  } while (continuationToken)
}
