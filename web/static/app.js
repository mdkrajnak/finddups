// Tab switching between groups and deletions views
function showTab(tabName) {
    // Hide all tab content
    document.querySelectorAll('.tab-content').forEach(el => {
        el.classList.add('hidden');
    });

    // Show selected tab content
    const view = document.getElementById(`view-${tabName}`);
    if (view) {
        view.classList.remove('hidden');
    }

    // Update tab button styles
    document.querySelectorAll('[id^="tab-"]').forEach(btn => {
        btn.classList.remove('tab-active', 'border-blue-500', 'text-blue-600');
        btn.classList.add('tab-inactive', 'border-transparent', 'text-gray-500');
    });

    const activeTab = document.getElementById(`tab-${tabName}`);
    if (activeTab) {
        activeTab.classList.remove('tab-inactive', 'border-transparent', 'text-gray-500');
        activeTab.classList.add('tab-active', 'border-blue-500', 'text-blue-600');
    }
}

// Confirmation dialog for executing deletions
function confirmExecute() {
    if (confirm('Are you sure you want to delete all marked files? This action cannot be undone.')) {
        // Trigger the actual execution via htmx
        htmx.ajax('POST', '/api/deletions/execute', {
            target: '#deletions-result',
            swap: 'innerHTML',
            values: { dry_run: false }
        }).then(() => {
            // Refresh the deletions list after execution
            htmx.ajax('GET', '/api/deletions', {
                target: '#deletions-list',
                swap: 'innerHTML'
            });
        });
    }
}

// Modal management
function openModal(groupId) {
    const modal = document.getElementById('review-modal');
    if (modal) {
        modal.classList.remove('hidden');

        // Load group details into modal via htmx
        htmx.ajax('GET', `/api/groups/${groupId}`, {
            target: '#modal-content',
            swap: 'innerHTML'
        });
    }
}

function closeModal() {
    const modal = document.getElementById('review-modal');
    if (modal) {
        modal.classList.add('hidden');
        document.getElementById('modal-content').innerHTML = '';
    }
}

// Close modal on background click
document.addEventListener('DOMContentLoaded', () => {
    const modal = document.getElementById('review-modal');
    if (modal) {
        modal.addEventListener('click', (e) => {
            if (e.target === modal) {
                closeModal();
            }
        });
    }
});

// Format bytes for display
function formatBytes(bytes) {
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    if (bytes === 0) return '0 B';
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return Math.round(bytes / Math.pow(1024, i) * 10) / 10 + ' ' + sizes[i];
}

// Format timestamp for display
function formatTime(unixSec) {
    const date = new Date(unixSec * 1000);
    return date.toLocaleString();
}
