-- +goose Up
CREATE TABLE local_administration_lock (
    id INT NOT NULL PRIMARY KEY CHECK (id = 1),
    version INT NOT NULL CHECK (version IN (0, 1))
);
INSERT INTO local_administration_lock (id, version) VALUES (1, 0);

-- +goose Down
DROP TABLE local_administration_lock;
