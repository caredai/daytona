/*
 * Copyright 2025 Daytona Platforms Inc.
 * SPDX-License-Identifier: AGPL-3.0
 */

import { S3Client, ListObjectsV2Command, DeleteObjectsCommand } from '@aws-sdk/client-s3'

/**
 * Delete all objects under a specific prefix (directory) in an S3 bucket.
 * Uses ListObjectsV2 only (no ListObjectVersions) for compatibility with
 * backends like Cloudflare R2 that do not implement object versioning APIs.
 *
 * @param s3 - S3 client instance
 * @param bucket - Bucket name
 * @param prefix - Prefix (directory path) to delete, should end with '/'
 */
export async function deleteS3Directory(s3: S3Client, bucket: string, prefix: string): Promise<void> {
  // Ensure prefix ends with '/' for proper directory matching
  const normalizedPrefix = prefix.endsWith('/') ? prefix : `${prefix}/`

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
