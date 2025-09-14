-- scripts/init-db.sql
CREATE TABLE IF NOT EXISTS files (
    file_id VARCHAR(255) PRIMARY KEY,
    file_name VARCHAR(255) NOT NULL,
    file_size BIGINT NOT NULL,
    chunk_count INT DEFAULT 0,
    status VARCHAR(50) DEFAULT 'uploading',
    user_id VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS chunks (
    chunk_id VARCHAR(64) PRIMARY KEY,
    file_id VARCHAR(255) REFERENCES files(file_id) ON DELETE CASCADE,
    chunk_index INT NOT NULL,
    chunk_size BIGINT NOT NULL,
    checksum VARCHAR(64),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS chunk_locations (
    chunk_id VARCHAR(64),
    node_id VARCHAR(50),
    storage_path TEXT,
    is_primary BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (chunk_id, node_id)
);

CREATE INDEX idx_files_status ON files(status);
CREATE INDEX idx_files_user ON files(user_id);
CREATE INDEX idx_chunks_file ON chunks(file_id);