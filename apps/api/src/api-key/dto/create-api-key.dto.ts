/*
 * Copyright 2025 Daytona Platforms Inc.
 * SPDX-License-Identifier: AGPL-3.0
 */

import { ArrayNotEmpty, IsArray, IsDate, IsEnum, IsNotEmpty, IsOptional, IsString } from 'class-validator'
import { ApiProperty, ApiPropertyOptional, ApiSchema } from '@nestjs/swagger'
import { OrganizationResourcePermission } from '../../organization/enums/organization-resource-permission.enum'
import { Type } from 'class-transformer'

@ApiSchema({ name: 'CreateApiKey' })
export class CreateApiKeyDto {
  @ApiProperty({
    description: 'The name of the API key',
    example: 'My API Key',
    required: true,
  })
  @IsNotEmpty()
  @IsString()
  name: string

  @ApiProperty({
    description: 'The list of organization resource permissions assigned to the API key',
    enum: OrganizationResourcePermission,
    isArray: true,
    required: true,
  })
  @IsArray()
  @ArrayNotEmpty()
  @IsEnum(OrganizationResourcePermission, { each: true })
  permissions: OrganizationResourcePermission[]

  @ApiPropertyOptional({
    description: 'When the API key expires',
    example: '2025-06-09T12:00:00.000Z',
    nullable: true,
  })
  @IsOptional()
  @Type(() => Date)
  @IsDate()
  expiresAt?: Date

  @ApiPropertyOptional({
    description: 'The API key',
    example: 'dtn_xxx',
    nullable: true,
  })
  @IsOptional()
  @IsString()
  apiKey?: string

  @ApiPropertyOptional({
    description: 'The organization ID',
    nullable: true,
  })
  @IsOptional()
  @IsString()
  organizationId?: string

  @ApiPropertyOptional({
    description: 'The user ID',
    nullable: true,
  })
  @IsOptional()
  @IsString()
  userId?: string
}
