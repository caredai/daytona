/*
 * Copyright 2025 Daytona Platforms Inc.
 * SPDX-License-Identifier: AGPL-3.0
 */

import { ApiProperty, ApiPropertyOptional, ApiSchema } from '@nestjs/swagger'
import { IsNotEmpty, IsOptional, IsString } from 'class-validator'

@ApiSchema({ name: 'CreateOrganization' })
export class CreateOrganizationDto {
  @ApiProperty({
    description: 'The name of organization',
    example: 'My Organization',
    required: true,
  })
  @IsString()
  @IsNotEmpty()
  name: string

  @ApiProperty({
    description: 'The ID of the default region for the organization',
    example: 'us',
    required: true,
  })
  @IsString()
  @IsNotEmpty()
  defaultRegionId: string

  @ApiPropertyOptional({
    description: 'The ID of the organization',
    example: '09a38176-ffc3-4403-846c-b1bd481dd78d',
    nullable: true,
  })
  @IsOptional()
  @IsString()
  id?: string

  @ApiPropertyOptional({
    description: 'The owner ID of the organization',
    example: 'user_xxx',
    nullable: true,
  })
  @IsOptional()
  @IsString()
  userId?: string
}
