-- Trigram indexes make case-insensitive partial name search (ILIKE '%term%') index-assisted.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE hospitals (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code       VARCHAR(50)  NOT NULL,
    name       VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT uq_hospitals_code UNIQUE (code),
    CONSTRAINT chk_hospitals_code_lowercase CHECK (code = lower(code))
);

-- A username only has to be unique inside its own hospital, because login always includes the hospital.
CREATE TABLE staff (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    hospital_id   BIGINT       NOT NULL REFERENCES hospitals (id) ON DELETE RESTRICT,
    username      VARCHAR(50)  NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT uq_staff_hospital_username UNIQUE (hospital_id, username),
    CONSTRAINT chk_staff_username_lowercase CHECK (username = lower(username))
);

-- One row per patient per hospital: the same person registered at two hospitals has two rows
-- (two different HNs), and each hospital's HIS stays the source of truth for its own rows.
-- Values copied from an HIS are stored as TEXT: the middleware must not reject a record because
-- one HIS formats a phone number or HN longer than another.
CREATE TABLE patients (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    hospital_id    BIGINT       NOT NULL REFERENCES hospitals (id) ON DELETE RESTRICT,
    patient_hn     TEXT         NOT NULL,
    national_id    TEXT,
    passport_id    TEXT,
    first_name_th  TEXT,
    middle_name_th TEXT,
    last_name_th   TEXT,
    first_name_en  TEXT,
    middle_name_en TEXT,
    last_name_en   TEXT,
    date_of_birth  DATE,
    phone_number   TEXT,
    email          TEXT,
    gender         CHAR(1),
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT uq_patients_hospital_hn UNIQUE (hospital_id, patient_hn),
    CONSTRAINT chk_patients_has_identity CHECK (national_id IS NOT NULL OR passport_id IS NOT NULL),
    CONSTRAINT chk_patients_gender CHECK (gender IN ('M', 'F'))
);

-- Identity numbers are unique inside a hospital but may repeat across hospitals.
CREATE UNIQUE INDEX uq_patients_hospital_national_id ON patients (hospital_id, national_id) WHERE national_id IS NOT NULL;
CREATE UNIQUE INDEX uq_patients_hospital_passport_id ON patients (hospital_id, passport_id) WHERE passport_id IS NOT NULL;

CREATE INDEX idx_patients_hospital_date_of_birth ON patients (hospital_id, date_of_birth);
CREATE INDEX idx_patients_hospital_email ON patients (hospital_id, lower(email));

CREATE INDEX idx_patients_first_name_th_trgm  ON patients USING gin (first_name_th gin_trgm_ops);
CREATE INDEX idx_patients_middle_name_th_trgm ON patients USING gin (middle_name_th gin_trgm_ops);
CREATE INDEX idx_patients_last_name_th_trgm   ON patients USING gin (last_name_th gin_trgm_ops);
CREATE INDEX idx_patients_first_name_en_trgm  ON patients USING gin (first_name_en gin_trgm_ops);
CREATE INDEX idx_patients_middle_name_en_trgm ON patients USING gin (middle_name_en gin_trgm_ops);
CREATE INDEX idx_patients_last_name_en_trgm   ON patients USING gin (last_name_en gin_trgm_ops);
