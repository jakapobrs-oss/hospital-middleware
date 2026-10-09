-- Hospitals are reference data: the middleware only serves hospitals it has been onboarded with.
INSERT INTO hospitals (code, name)
VALUES ('hospital-a', 'Hospital A'),
       ('hospital-b', 'Hospital B')
ON CONFLICT (code) DO NOTHING;
