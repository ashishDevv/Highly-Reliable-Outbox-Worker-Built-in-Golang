package utils

import (
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgtype"
)

func ConvertGoogleUUIDToPgtypeUUID(googleUUID uuid.UUID) pgtype.UUID {
    return pgtype.UUID{
        Bytes: googleUUID,
        Valid: true,
    }
}