package main

func commandErrorMessage(err error) string {
	switch err.Error() {
	case "desktop_not_running_start_application_or_service":
		return "KI-Basis läuft derzeit nicht. Öffne KI-Basis erneut. Im Serverbetrieb starte den Dienst „KI-Basis-Desktop“ in der Windows-Dienstverwaltung und öffne danach die Serververwaltung."
	case "desktop_already_running_or_locked":
		return "Eine KI-Basis-Instanz startet gerade oder verwendet diesen Datenordner. Warte einen Moment und öffne KI-Basis erneut. Beende eine hängende Instanz über den Task-Manager, ohne den Datenordner zu löschen."
	case "desktop_storage_unavailable", "desktop_session_storage_failed":
		return "KI-Basis kann seinen Datenordner nicht verwenden. Prüfe freien Speicherplatz und die Schreibrechte deines Kontos. Im Serverbetrieb benötigt das Dienstkonto Zugriff auf den ProgramData-Ordner. Vorhandene Daten bleiben erhalten."
	case "desktop_gateway_port_in_use", "desktop_port_in_use", "desktop_engine_port_unavailable":
		return "Ein benötigter lokaler Port wird bereits verwendet. Schließe eine ältere KI-Basis-Instanz oder den anderen Dienst und starte KI-Basis erneut. Ändere keine Firewallregel: Die Verwaltung verwendet nur diesen Rechner."
	case "desktop_admin_credentials_unavailable":
		return "Die gespeicherten Verwaltungszugänge passen nicht zur Konfiguration. Verwende die bestehende Installation oder eine geprüfte Sicherung. Lösche keine Schlüssel- oder Zertifikatsdateien."
	case "desktop_configuration_invalid", "desktop_gateway_configuration_invalid", "desktop_gateway_must_be_loopback", "desktop_agent_settings_invalid", "desktop_agent_identity_invalid", "desktop_agent_identity_mismatch":
		return "Die gespeicherte KI-Basis-Einrichtung ist ungültig oder gehört zu einer anderen Installation. Stelle eine passende Sicherung wieder her oder lass die Konfiguration prüfen. Vorhandene Identitäten werden nicht überschrieben."
	case "desktop_audit_unavailable":
		return "KI-Basis kann sein Audit nicht öffnen. Prüfe freien Speicherplatz und ob eine andere Instanz denselben Datenordner verwendet. Sichere vorhandene Daten, bevor du eine Reparatur versuchst."
	case "desktop_listener_failed", "desktop_gateway_failed", "desktop_agent_start_failed":
		return "Ein lokaler KI-Basis-Dienst konnte nicht gestartet werden. Starte KI-Basis erneut und prüfe, ob eine ältere Instanz läuft. Falls der Fehler bleibt, lass die Installation prüfen; deine Daten werden nicht gelöscht."
	case "desktop_invalid_options", "desktop_directory_must_be_absolute":
		return "Die Startoptionen von KI-Basis sind ungültig. Verwende den Startmenüeintrag oder führe das aktuelle Setup zur Reparatur der Programmdateien erneut aus."
	default:
		// Errors may originate in third-party code or include private paths. The
		// graphical fallback deliberately does not echo that data into a dialog.
		return "KI-Basis konnte nicht gestartet werden. Öffne die Anwendung über den Startmenüeintrag erneut. Falls der Fehler bleibt, prüfe die Installation und sichere vorhandene Daten vor einer Reparatur."
	}
}
