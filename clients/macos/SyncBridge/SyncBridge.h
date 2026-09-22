#ifndef DISCODRIVE_SYNC_BRIDGE_H
#define DISCODRIVE_SYNC_BRIDGE_H
char *DDFullSyncSetLogPath(const char *path);
char *DDFullSyncStart(const char *configuration);
void DDFullSyncStop(void);
void DDFullSyncConfirmDeletion(void);
char *DDFullSyncStatus(void);
void DDFullSyncFree(char *value);
#endif
