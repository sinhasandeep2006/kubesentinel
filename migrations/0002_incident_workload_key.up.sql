-- Incidents are now keyed by workload + container + detector, not by pod.
CREATE INDEX incidents_workload_key_idx ON incidents (namespace, workload_kind, workload_name, container, detector);
