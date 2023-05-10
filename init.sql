/*
When initializing database locally:
CREATE USER seili WITH PASSWORD 'seilipassword';
CREATE DATABASE seili_osprey_nest;
GRANT ALL PRIVILEGES ON DATABASE seili_osprey_nest TO seili;
*/

CREATE TABLE IF NOT EXISTS classes (
	id serial PRIMARY KEY,
    class_id INT,
	label TEXT UNIQUE NOT NULL,
	description TEXT
);

CREATE TABLE IF NOT EXISTS detection_event (
	id serial PRIMARY KEY,
	class INT,
    count INT,
	created TIMESTAMP NOT NULL DEFAULT NOW(),
    FOREIGN KEY (class) REFERENCES classes (id)
);

CREATE TABLE IF NOT EXISTS detection (
    id serial PRIMARY KEY,
    confidence INT, 
    location_top INT,
    location_left INT,
    width INT,
    height INT,
    event INT,
    FOREIGN KEY (event) REFERENCES detection_event (id)
);

CREATE TABLE IF NOT EXISTS stream (
    id serial PRIMARY KEY,
    name TEXT,
    link TEXT,
    address TEXT
);

CREATE TABLE IF NOT EXISTS observer (
    id serial PRIMARY KEY,
    name TEXT,
    email TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS subscription (
    id serial PRIMARY KEY,
    observer_id INT,
    stream_id INT,
    alert BOOLEAN DEFAULT FALSE,
    alert_trigger TEXT,
    alert_interval TEXT,
    confidence DECIMAL,
    FOREIGN KEY (observer_id) REFERENCES observer (id),
    FOREIGN KEY (stream_id) REFERENCES stream (id)
);

CREATE TABLE IF NOT EXISTS alert (
    id serial PRIMARY KEY,
    detection_event_id INT,
    subscription_id INT,
    created TIMESTAMP,
    FOREIGN KEY (detection_event_id) REFERENCES detection_event (id),
    FOREIGN KEY (subscription_id) REFERENCES subscription (id)
);

INSERT INTO classes (class_id, label, description) VALUES (1, 'osprey', 'An osprey is a medium-large fish-eating bird of prey.');
INSERT INTO stream(name,address,link) VALUES('location', 'rtsp://user:password@address','https://www.youtube.com/watch?v=stream_id');
INSERT INTO observer(name,email) VALUES('observer', 'test@mail');
INSERT INTO subscription(observer_id,stream_id,alert,alert_trigger,alert_interval,confidence) VALUES(1,1,'t','deafult','15m',0.9);
