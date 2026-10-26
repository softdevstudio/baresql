-- Runs as a superuser in database baresql_fx_clinic.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA clinic AUTHORIZATION clinic_owner;
GRANT USAGE ON SCHEMA clinic TO clinic_staff, clinic_research, clinic_billing, clinic_lab;
SET ROLE clinic_owner;

CREATE TABLE clinic.patients (
  id integer PRIMARY KEY,
  first_name text,
  last_name text,
  date_of_birth date,
  national_id text,
  email text,
  phone text,
  home_address text,
  insurance_number text
);
INSERT INTO clinic.patients VALUES
{{- range $i := seq 1 30}}
  ({{$i}}, '{{pick "Maria" "Jon" "Aiko" "Omar"}}', '{{pick "Garcia" "Berg" "Sato" "Haddad"}}', '19{{pick "62" "75" "88" "91"}}-0{{pick "1" "4" "7"}}-1{{pick "2" "5" "8"}}',
   '{{dni}}', 'patient{{$i}}@mail.example', '{{phone}}', '{{$i}} {{pick "High Street" "Calle Mayor" "Ringstrasse"}}', 'INS-{{digits 8}}'){{if lt $i 30}},{{else}};{{end}}
{{- end}}

-- Doctors read visits through the partitioned table; research has a grant
-- on one partition only.
CREATE TABLE clinic.visits (id integer, patient_id integer, visit_date date, diagnosis text, notes text)
  PARTITION BY RANGE (visit_date);
CREATE TABLE clinic.visits_2025 PARTITION OF clinic.visits FOR VALUES FROM ('2025-01-01') TO ('2026-01-01');
CREATE TABLE clinic.visits_2026 PARTITION OF clinic.visits FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
INSERT INTO clinic.visits SELECT g, 1 + g % 30, '2025-01-01'::date + g * 10,
  (ARRAY['J45 asthma', 'E11 diabetes', 'I10 hypertension'])[1 + g % 3], 'follow up in three months'
  FROM generate_series(1, 50) g;

-- Lab results arrive as JSON with the patient inside.
CREATE TABLE clinic.lab_results (id integer PRIMARY KEY, patient_id integer, payload jsonb);
INSERT INTO clinic.lab_results VALUES
{{- range $i := seq 1 20}}
  ({{$i}}, {{$i}}, '{"patient": {"name": "Patient {{$i}}", "email": "patient{{$i}}@mail.example"}, "results": [{"test": "HbA1c", "value": 6.{{$i}}}]}'){{if lt $i 20}},{{else}};{{end}}
{{- end}}

CREATE TABLE clinic.appointments (id integer PRIMARY KEY, patient_id integer, starts_at timestamptz, room text);
INSERT INTO clinic.appointments SELECT g, g, '2026-11-01 09:00'::timestamptz + g * interval '30 minutes', 'Room ' || (g % 4)
  FROM generate_series(1, 20) g;

CREATE MATERIALIZED VIEW clinic.diagnosis_stats AS
  SELECT diagnosis, count(*) AS visits FROM clinic.visits GROUP BY diagnosis;

GRANT SELECT ON clinic.appointments TO clinic_staff;
GRANT SELECT ON clinic.patients, clinic.visits, clinic.lab_results TO clinic_doctor;
GRANT SELECT (id, first_name, last_name, phone) ON clinic.patients TO clinic_frontdesk;
GRANT SELECT ON clinic.diagnosis_stats, clinic.visits_2025 TO clinic_research;
GRANT SELECT (id, email, insurance_number) ON clinic.patients TO clinic_billing WITH GRANT OPTION;
GRANT SELECT ON clinic.lab_results TO clinic_lab;
RESET ROLE;

GRANT EXECUTE ON FUNCTION pg_catalog.pg_read_file(text) TO clinic_it;
CREATE FOREIGN DATA WRAPPER clinic_fdw;
CREATE SERVER clinic_lab_server FOREIGN DATA WRAPPER clinic_fdw;
GRANT USAGE ON FOREIGN SERVER clinic_lab_server TO clinic_lab;
CREATE USER MAPPING FOR clinic_lab SERVER clinic_lab_server OPTIONS (user 'lab', password 'not-a-real-password');
